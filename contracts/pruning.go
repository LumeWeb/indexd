package contracts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/indexd/hosts"
	"go.uber.org/zap"
)

// HostKey returns the public key of the host.
func (c *hostClient) HostKey() types.PublicKey {
	return c.hostKey
}

func (c *hostClient) SectorRoots(ctx context.Context, hostPrices proto.HostPrices, contractID types.FileContractID, offset, length uint64) (rhp.RPCSectorRootsResult, error) {
	// fetch revision and check if it meets the requirements
	rev, err := rhp.RPCLatestRevision(ctx, c.client, contractID)
	if err != nil {
		return rhp.RPCSectorRootsResult{}, fmt.Errorf("failed to fetch latest revision: %w", err)
	} else if !rev.Revisable {
		return rhp.RPCSectorRootsResult{}, errors.New("contract is not revisable")
	} else if rev.Contract.RenterOutput.Value.IsZero() {
		return rhp.RPCSectorRootsResult{}, errors.New("contract is out of funds")
	}

	// fetch contract sectors
	revision := rhp.ContractRevision{ID: contractID, Revision: rev.Contract}
	return rhp.RPCSectorRoots(ctx, c.client, c.cm.TipState(), hostPrices, c.signer, revision, offset, length)
}

func (c *hostClient) FreeSectors(ctx context.Context, hostPrices proto.HostPrices, contractID types.FileContractID, indices []uint64) (rhp.RPCFreeSectorsResult, error) {
	// fetch revision and check if it meets the requirements
	rev, err := rhp.RPCLatestRevision(ctx, c.client, contractID)
	if err != nil {
		return rhp.RPCFreeSectorsResult{}, fmt.Errorf("failed to fetch latest revision: %w", err)
	} else if !rev.Revisable {
		return rhp.RPCFreeSectorsResult{}, errors.New("contract is not revisable")
	} else if rev.Contract.RenterOutput.Value.IsZero() {
		return rhp.RPCFreeSectorsResult{}, errors.New("contract is out of funds")
	}

	// free sectors
	revision := rhp.ContractRevision{ID: contractID, Revision: rev.Contract}
	return rhp.RPCFreeSectors(ctx, c.client, c.signer, c.cm.TipState(), hostPrices, revision, indices)
}

func (cm *ContractManager) performContractPruning(ctx context.Context, log *zap.Logger) error {
	start := time.Now()
	log = log.Named("contractpruning")

	// prune sectors on usable hosts with active contracts
	opts := []hosts.HostQueryOpt{
		hosts.WithUsable(true),
		hosts.WithBlocked(false),
		hosts.WithActiveContracts(true),
	}

	const (
		batchSize        = 50
		sectorsBatchSize = (1 << 40) / proto.SectorSize
	)

	var wg sync.WaitGroup
	sema := make(chan struct{}, 50)
	defer close(sema)

	for offset := 0; ctx.Err() == nil; offset += batchSize {
		// fetch hosts
		batch, err := cm.store.Hosts(ctx, offset, batchSize, opts...)
		if err != nil {
			return fmt.Errorf("failed to fetch hosts for pruning: %w", err)
		}

		// prune contracts in parallel
		for _, h := range batch {
			select {
			case <-ctx.Done():
				break
			case sema <- struct{}{}:
			}

			wg.Add(1)
			go func(ctx context.Context, host hosts.Host, hostLog *zap.Logger) {
				defer func() {
					<-sema
					wg.Done()
				}()

				err = cm.performContractPruningOnHost(ctx, host, hostLog)
				if err != nil {
					hostLog.Debug("failed to prune contracts", zap.Error(err))
				}
			}(ctx, h, log.With(zap.Stringer("hostKey", h.PublicKey)))
		}

		// break if hosts are exhausted
		if len(batch) < batchSize {
			break
		}
	}

	wg.Wait()

	log.Debug("pruning finished", zap.Duration("duration", time.Since(start)))
	return ctx.Err()
}

func (cm *ContractManager) performContractPruningOnHost(ctx context.Context, host hosts.Host, hostLog *zap.Logger) error {
	// refresh prices if necessary
	ts := host.Settings.Prices.ValidUntil
	if !host.Usability.Usable() || time.Until(ts) < 30*time.Minute {
		host, err := cm.scanner.ScanHost(ctx, host.PublicKey)
		if err != nil {
			return fmt.Errorf("failed to scan host: %w", err)
		} else if !host.IsGood() {
			hostLog.Debug("host is not good for pinning", zap.Bool("blocked", host.Blocked), zap.Bool("usable", host.Usability.Usable()), zap.Bool("networks", len(host.Networks) > 0))
			return fmt.Errorf("host is not good: %w", err)
		}
	}

	// dial the host
	client, err := cm.dialer.Dial(ctx, host.PublicKey, host.SiamuxAddr())
	if err != nil {
		return fmt.Errorf("failed to dial host: %w", err)
	}
	defer client.Close()

	// fetch contract ids
	contracts, err := cm.store.ContractsForPruning(ctx, host.PublicKey, time.Now().Add(-time.Hour*24))
	if err != nil {
		return fmt.Errorf("failed to fetch contracts for pruning: %w", err)
	} else if len(contracts) == 0 {
		hostLog.Debug("no contracts for pruning")
		return nil
	}

	hostLog.Debug("pruning contracts on host", zap.Int("contracts", len(contracts)))
	for _, contract := range contracts {
		select {
		case <-ctx.Done():
			break
		default:
		}

		n, err := cm.pruneContract(ctx, client, host.Settings.Prices, contract)
		if err != nil {
			hostLog.Debug("failed to prune contract", zap.Error(err))
			continue
		} else if n > 0 {
			hostLog.Debug("pruned contract", zap.Stringer("contractID", contract), zap.Int("sectors", n), zap.Int("bytes", n*proto.SectorSize))
		}

		err = cm.store.MarkPruned(ctx, contract)
		if err != nil {
			hostLog.Debug("failed to mark contract as pruned", zap.Error(err))
		}
	}

	return nil
}

func (cm *ContractManager) pruneContract(ctx context.Context, client HostClient, hostPrices proto.HostPrices, contractID types.FileContractID) (int, error) {
	const (
		oneTB          = 1 << 40
		sectorsPerTB   = oneTB / proto.SectorSize
		rootsBatchSize = 10000
	)

	contract, err := cm.store.ContractElement(ctx, contractID)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch contract: %w", err)
	}
	contractSectors := contract.V2FileContract.Filesize / proto.SectorSize

	var pruned int
	for offset := uint64(0); offset < contractSectors; offset += sectorsPerTB {
		length := min(sectorsPerTB, contractSectors-offset)
		res, err := client.SectorRoots(ctx, hostPrices, contractID, offset, length)
		if err != nil {
			return pruned, fmt.Errorf("failed to fetch contract sectors: %w", err)
		} else if len(res.Roots) == 0 {
			continue
		}

		// TODO: handle usage

		prunable := make(map[types.Hash256]struct{}, len(res.Roots))
		for start := 0; start < len(res.Roots); start += rootsBatchSize {
			end := min(start+rootsBatchSize, len(res.Roots))
			batch, err := cm.store.PrunableContractRoots(ctx, contractID, res.Roots[start:end])
			if err != nil {
				return pruned, fmt.Errorf("failed to fetch prunable contract roots: %w", err)
			}
			for _, root := range batch {
				prunable[root] = struct{}{}
			}

			// TODO: handle usage
		}

		var indices []uint64
		for i, root := range res.Roots {
			if _, found := prunable[root]; found {
				indices = append(indices, uint64(i))
			}
		}
		if len(indices) == 0 {
			continue
		}

		pruned += len(indices)
		_, err = client.FreeSectors(ctx, hostPrices, contractID, indices)
		if err != nil {
			return pruned, fmt.Errorf("failed to prune contract sectors: %w", err)
		}
	}

	return pruned, nil
}
