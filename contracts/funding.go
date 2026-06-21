package contracts

import (
	"context"
	"fmt"
	"math"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	rhp "go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/indexd/accounts"
	"go.sia.tech/indexd/hosts"
	"go.uber.org/zap"
)

const (
	// attachValidity is the replay window for pool attachment signatures.
	attachValidity = 5 * time.Minute

	// serviceAccountFundTargetBytes is the number of bytes used to calculate
	// the fund target for a host's service account. We fund accounts to cover
	// this amount of read and write usage. It roughly comes down to uploading
	// and downloading to and from a host at ~1Gbps for a period of 2 minutes.
	// With 30 good hosts, this results in about 30Gbps of maximum theoretical
	// throughput.
	serviceAccountFundTargetBytes = uint64(16 << 30) // 16 GiB
)

// AttachPools attaches all pending pool attachments for the given host.
func (cm *ContractManager) AttachPools(ctx context.Context, hostKey types.PublicKey, log *zap.Logger) error {
	var exhausted bool
	for !exhausted {
		// fetch pending attachments
		pending, err := cm.accounts.PendingPoolAttachments(hostKey, proto.MaxAccountBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch pending attachments: %w", err)
		} else if len(pending) == 0 {
			break
		} else if len(pending) < proto.MaxAccountBatchSize {
			exhausted = true
		}

		// construct attach inputs
		inputs := make([]rhp.PoolAttachInput, len(pending))
		for i, p := range pending {
			inputs[i] = rhp.PoolAttachInput{
				Account: p.AccountKey,
				PoolKey: p.PoolKey,
			}
		}

		// attach pools
		if err := cm.accountFunder.AttachPools(ctx, hostKey, inputs, attachValidity); err != nil {
			return fmt.Errorf("failed to attach pools: %w", err)
		} else if err := cm.accounts.InsertPoolAttachments(hostKey, pending); err != nil {
			return fmt.Errorf("failed to record attachments: %w", err)
		}

		log.Debug("attached pools to accounts", zap.Int("count", len(pending)))
	}

	return cm.attachSharingPools(ctx, hostKey, log)
}

// attachSharingPools attaches each funded pool's derived sharing account on the
// host.
func (cm *ContractManager) attachSharingPools(ctx context.Context, hostKey types.PublicKey, log *zap.Logger) error {
	var exhausted bool
	for !exhausted {
		sharing, err := cm.accounts.SharingPoolAttachments(hostKey, proto.MaxAccountBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch sharing pool attachments: %w", err)
		} else if len(sharing) == 0 {
			break
		} else if len(sharing) < proto.MaxAccountBatchSize {
			exhausted = true
		}

		// construct attach inputs
		inputs := make([]rhp.PoolAttachInput, len(sharing))
		for i, p := range sharing {
			inputs[i] = rhp.PoolAttachInput{
				Account: p.AccountKey,
				PoolKey: p.PoolKey,
			}
		}

		// attach sharing accounts and record them
		if err := cm.accountFunder.AttachPools(ctx, hostKey, inputs, attachValidity); err != nil {
			return fmt.Errorf("failed to attach sharing pools: %w", err)
		} else if err := cm.accounts.MarkSharingPoolsAttached(hostKey, sharing); err != nil {
			return fmt.Errorf("failed to record sharing attachments: %w", err)
		}

		log.Debug("attached sharing accounts to pools", zap.Int("count", len(sharing)))
	}
	return nil
}

// ContractFundTarget calculates the fund target for a contract on the given
// host. For hosts that support pools it sums per-quota pool targets, for
// legacy hosts it sums per-quota per-account targets. One active pool counts
// the same as one active legacy account. Service accounts are always
// included.
func (cm *ContractManager) ContractFundTarget(ctx context.Context, host hosts.Host, minAllowance types.Currency) (types.Currency, error) {
	threshold := time.Now().Add(-accounts.AccountActivityThreshold)

	var infos []accounts.QuotaFundInfo
	var err error
	if host.HasPoolSupport() {
		infos, err = cm.accounts.PoolFundingInfo(threshold)
	} else {
		infos, err = cm.accounts.AccountFundingInfo(threshold)
	}
	if err != nil {
		return types.ZeroCurrency, err
	}

	var target types.Currency
	for _, info := range infos {
		fullStorage := min(info.FullStorage, info.Active)
		upload := info.Active - fullStorage
		t := accounts.HostFundTarget(host, info.FundTargetBytes).Mul64(upload)
		t = t.Add(accounts.HostReadFundTarget(host, info.FundTargetBytes).Mul64(fullStorage))
		target = target.Add(t)
	}

	// service accounts
	target = target.Add(accounts.HostFundTarget(host, serviceAccountFundTargetBytes).Mul64(uint64(len(cm.accounts.ServiceAccounts(host.PublicKey)))))

	if target.Cmp(minAllowance) < 0 {
		target = minAllowance
	}

	return target, nil
}

// FundAccounts funds individual accounts on legacy hosts. For hosts that support pools, FundPools is used.
func (cm *ContractManager) FundAccounts(ctx context.Context, host hosts.Host, contractIDs []types.FileContractID, quotas []accounts.Quota, log *zap.Logger) error {
	if len(contractIDs) == 0 {
		log.Debug("no contracts provided")
		return nil
	} else if host.Blocked {
		log.Debug("host is blocked")
		return nil
	} else if !host.Usability.Usable() {
		log.Debug("host is not usable")
		return nil
	} else if host.HasPoolSupport() {
		return nil
	}

	// fund service accounts
	serviceAccounts := cm.accounts.ServiceAccounts(host.PublicKey)
	if len(serviceAccounts) > 0 {
		fundTarget := accounts.HostFundTarget(host, serviceAccountFundTargetBytes)
		funded, _, deposits, err := cm.accountFunder.FundAccounts(ctx, host, contractIDs, serviceAccounts, fundTarget, log)
		if err != nil {
			return fmt.Errorf("failed to fund service accounts: %w", err)
		}

		recordFundingEvents(cm.accounts, host, deposits, log)

		if err := cm.accounts.UpdateServiceAccounts(serviceAccounts[:funded], fundTarget); err != nil {
			cm.log.Warn("failed to update service account balances", zap.Error(err))
		}
		return nil
	}

	threshold := time.Now().Add(-accounts.AccountActivityThreshold)
OUTER:
	for _, quota := range quotas {
		fundTarget := accounts.HostFundTarget(host, quota.FundTargetBytes)
		readFundTarget := accounts.HostReadFundTarget(host, quota.FundTargetBytes)
		if fundTarget.IsZero() && readFundTarget.IsZero() {
			continue
		}

		var exhausted bool
		for !exhausted {
			accs, err := cm.accounts.AccountsForFunding(host.PublicKey, quota.Key, threshold, accounts.AccountFundBatch)
			if err != nil {
				return fmt.Errorf("failed to fetch accounts for funding: %w", err)
			} else if len(accs) < accounts.AccountFundBatch {
				exhausted = true
			}
			if len(accs) == 0 {
				break
			}

			// split accounts by storage state
			var uploadAccs, fullStorageAccs []accounts.HostAccount
			for _, acc := range accs {
				if acc.FullStorage {
					fullStorageAccs = append(fullStorageAccs, acc)
				} else {
					uploadAccs = append(uploadAccs, acc)
				}
			}

			// fund each group with the appropriate target
			for _, batch := range []struct {
				accs   []accounts.HostAccount
				target types.Currency
			}{
				{uploadAccs, fundTarget},
				{fullStorageAccs, readFundTarget},
			} {
				if len(batch.accs) == 0 || batch.target.IsZero() {
					continue
				}

				// fund accounts
				funded, drained, deposits, err := cm.accountFunder.FundAccounts(ctx, host, contractIDs, batch.accs, batch.target, log)
				if err != nil {
					return fmt.Errorf("failed to fund accounts: %w", err)
				}

				recordFundingEvents(cm.accounts, host, deposits, log)

				// update funded accounts
				accounts.UpdateFundedAccounts(batch.accs, funded, cm.maxAccountFundingBackoff)
				if err := cm.accounts.UpdateHostAccounts(batch.accs); err != nil {
					return fmt.Errorf("failed to update accounts: %w", err)
				}

				contractIDs = contractIDs[drained:]
				if len(contractIDs) == 0 {
					log.Debug("not all accounts could be funded, no more contracts available", zap.String("quota", quota.Key))
					break OUTER
				}
			}
		}
	}

	return nil
}

// FundServiceAccounts funds service accounts on any host.
func (cm *ContractManager) FundServiceAccounts(ctx context.Context, host hosts.Host, contractIDs []types.FileContractID, log *zap.Logger) error {
	if len(contractIDs) == 0 {
		log.Debug("no contracts provided")
		return nil
	} else if host.Blocked {
		log.Debug("host is blocked")
		return nil
	} else if !host.Usability.Usable() {
		log.Debug("host is not usable")
		return nil
	}

	// fund service accounts
	serviceAccounts := cm.accounts.ServiceAccounts(host.PublicKey)
	if len(serviceAccounts) > 0 {
		fundTarget := accounts.HostFundTarget(host, serviceAccountFundTargetBytes)
		funded, _, deposits, err := cm.accountFunder.FundAccounts(ctx, host, contractIDs, serviceAccounts, fundTarget, log)
		if err != nil {
			return fmt.Errorf("failed to fund service accounts: %w", err)
		}

		recordFundingEvents(cm.accounts, host, deposits, log)

		if err := cm.accounts.UpdateServiceAccounts(serviceAccounts[:funded], fundTarget); err != nil {
			cm.log.Warn("failed to update service account balances", zap.Error(err))
		}
	}
	return nil
}

// FundPools attempts to fund all pools for the given host key. It does so
// using the provided contract IDs, which are used in the order they're given.
// Only hosts that support pools (>= 5.1.0) are funded.
func (cm *ContractManager) FundPools(ctx context.Context, host hosts.Host, contractIDs []types.FileContractID, quotas []accounts.Quota, log *zap.Logger) error {
	if !host.HasPoolSupport() {
		log.Debug("host does not support pools", zap.Stringer("version", host.Settings.ProtocolVersion))
		return nil
	} else if len(contractIDs) == 0 {
		log.Debug("no contracts provided")
		return nil
	} else if host.Blocked {
		log.Debug("host is blocked")
		return nil
	} else if !host.Usability.Usable() {
		log.Debug("host is not usable")
		return nil
	}

	threshold := time.Now().Add(-accounts.AccountActivityThreshold)
OUTER:
	for _, quota := range quotas {
		fundTarget := accounts.HostFundTarget(host, quota.FundTargetBytes)
		readFundTarget := accounts.HostReadFundTarget(host, quota.FundTargetBytes)
		if fundTarget.IsZero() && readFundTarget.IsZero() {
			continue
		}

		var exhausted bool
		for !exhausted {
			pools, err := cm.accounts.PoolsForFunding(host.PublicKey, quota.Key, threshold, proto.MaxAccountBatchSize)
			if err != nil {
				return fmt.Errorf("failed to fetch pools for funding: %w", err)
			} else if len(pools) < proto.MaxAccountBatchSize {
				exhausted = true
			}
			if len(pools) == 0 {
				break
			}

			// split pools by storage state
			var uploadPools, fullStoragePools []accounts.HostPool
			for _, p := range pools {
				if p.FullStorage {
					fullStoragePools = append(fullStoragePools, p)
				} else {
					uploadPools = append(uploadPools, p)
				}
			}

			for _, batch := range []struct {
				pools  []accounts.HostPool
				target types.Currency
			}{
				{uploadPools, fundTarget},
				{fullStoragePools, readFundTarget},
			} {
				if len(batch.pools) == 0 || batch.target.IsZero() {
					continue
				}

				funded, drained, deposits, err := cm.accountFunder.FundPools(ctx, host, contractIDs, batch.pools, batch.target, log)
				if err != nil {
					return fmt.Errorf("failed to fund pools: %w", err)
				}

				recordPoolFundingEvents(cm.accounts, host, deposits, batch.pools, log)

				accounts.UpdateFundedPools(batch.pools, funded, cm.maxAccountFundingBackoff)
				if err := cm.accounts.UpdateHostPools(batch.pools); err != nil {
					return fmt.Errorf("failed to update pools: %w", err)
				}

				contractIDs = contractIDs[drained:]
				if len(contractIDs) == 0 {
					log.Debug("not all pools could be funded, no more contracts available", zap.String("quota", quota.Key))
					break OUTER
				}
			}
		}
	}

	return nil
}

// estimatedBytes estimates the bytes covered by a single operation type (upload or download)
// from the actual SC deposited. The deposit covers both read and write usage in a 50/50 split,
// so we divide by 2 to approximate each operation's share, then convert SC to sectors and bytes.
func estimatedBytes(amount types.Currency, costPerSector types.Currency) uint64 {
	if costPerSector.IsZero() {
		return 0
	}
	divisor := costPerSector.Mul64(2)
	if divisor.IsZero() {
		return 0
	}
	result := amount.Mul64(proto.SectorSize).Div(divisor)
	if !result.Big().IsUint64() {
		return math.MaxInt64
	}
	return result.Big().Uint64()
}

func recordFundingEvents(am AccountManager, host hosts.Host, deposits []FundedDeposit, log *zap.Logger) {
	recordFundingEventsTyped(am, host, deposits, accounts.FundingTypeAccount, nil, log)
}

// recordPoolFundingEvents records funding events for pool deposits. It maps
// each deposit's account key back to the pool DB ID to set the pool_id column.
func recordPoolFundingEvents(am AccountManager, host hosts.Host, deposits []FundedDeposit, pools []accounts.HostPool, log *zap.Logger) {
	if len(deposits) == 0 {
		return
	}
	// build a map from pool public key to pool DB ID
	poolIDs := make(map[proto.Account]int, len(pools))
	for _, p := range pools {
		poolIDs[proto.Account(p.PoolKey.PublicKey())] = p.ID
	}

	writeCostPerSector := host.Settings.Prices.RPCWriteSectorCost(proto.SectorSize).RenterCost()
	readCostPerSector := host.Settings.Prices.RPCReadSectorCost(proto.SectorSize).RenterCost()
	events := make([]accounts.FundingEvent, 0, len(deposits))
	for _, d := range deposits {
		poolID := poolIDs[d.Deposit.Account]
		events = append(events, accounts.FundingEvent{
			AccountKey:             d.Deposit.Account,
			HostKey:                host.PublicKey,
			ContractID:             d.ContractID,
			AmountSC:               d.Deposit.Amount,
			EstimatedUploadBytes:   estimatedBytes(d.Deposit.Amount, writeCostPerSector),
			EstimatedDownloadBytes: estimatedBytes(d.Deposit.Amount, readCostPerSector),
			FundType:               accounts.FundingTypePool,
			PoolID:                 &poolID,
		})
	}
	if err := am.RecordFundingEvents(events); err != nil {
		log.Warn("failed to record funding events", zap.Error(err))
	}
}

func recordFundingEventsTyped(am AccountManager, host hosts.Host, deposits []FundedDeposit, fundType string, poolID *int, log *zap.Logger) {
	if len(deposits) == 0 {
		return
	}
	writeCostPerSector := host.Settings.Prices.RPCWriteSectorCost(proto.SectorSize).RenterCost()
	readCostPerSector := host.Settings.Prices.RPCReadSectorCost(proto.SectorSize).RenterCost()
	events := make([]accounts.FundingEvent, 0, len(deposits))
	for _, d := range deposits {
		events = append(events, accounts.FundingEvent{
			AccountKey:             d.Deposit.Account,
			HostKey:                host.PublicKey,
			ContractID:             d.ContractID,
			AmountSC:               d.Deposit.Amount,
			EstimatedUploadBytes:   estimatedBytes(d.Deposit.Amount, writeCostPerSector),
			EstimatedDownloadBytes: estimatedBytes(d.Deposit.Amount, readCostPerSector),
			FundType:               fundType,
			PoolID:                 poolID,
		})
	}
	if err := am.RecordFundingEvents(events); err != nil {
		log.Warn("failed to record funding events", zap.Error(err))
	}
}
