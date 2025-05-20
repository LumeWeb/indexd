package contracts

import (
	"context"
	"errors"
	"maps"
	"net"
	"slices"
	"sort"
	"testing"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/coreutils/rhp/v4/siamux"
	"go.sia.tech/indexd/hosts"
	"go.uber.org/zap"
)

type sectorRootsCall struct {
	hostPrices proto.HostPrices
	contractID types.FileContractID
	offset     uint64
	length     uint64
}

type freeSectorsCall struct {
	hostPrices proto.HostPrices
	contractID types.FileContractID
	indices    []uint64
}

func (s *storeMock) ContractsForPruning(ctx context.Context, hk types.PublicKey, maxLastPrune time.Time) ([]types.FileContractID, error) {
	var contracts []Contract
	for _, c := range s.contracts {
		if c.HostKey == hk && !c.RemainingAllowance.IsZero() && c.LastPrune.Before(maxLastPrune) {
			contracts = append(contracts, c)
		}
	}
	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].Size > contracts[j].Size
	})

	out := make([]types.FileContractID, len(contracts))
	for i, c := range contracts {
		out[i] = c.ID
	}
	return out, nil
}

func (s *storeMock) MarkPruned(ctx context.Context, contractID types.FileContractID) error {
	for i, c := range s.contracts {
		if c.ID == contractID {
			s.contracts[i].LastPrune = time.Now()
			return nil
		}
	}
	return ErrNotFound
}

func (s *storeMock) PrunableContractRoots(ctx context.Context, contractID types.FileContractID, roots []types.Hash256) ([]types.Hash256, error) {
	lookup := make(map[types.Hash256]struct{}, len(roots))
	for _, root := range roots {
		lookup[root] = struct{}{}
	}
	for _, sectors := range s.sectors {
		for _, sector := range sectors {
			if sector.contractID != nil && *sector.contractID == contractID {
				delete(lookup, sector.root)
			}
		}
	}
	return slices.Collect(maps.Keys(lookup)), nil
}

func (c *hostClientMock) SectorRoots(ctx context.Context, hostPrices proto.HostPrices, contractID types.FileContractID, offset, length uint64) (rhp.RPCSectorRootsResult, error) {
	c.sectorRootsCalls = append(c.sectorRootsCalls, sectorRootsCall{
		hostPrices: hostPrices,
		contractID: contractID,
		offset:     offset,
		length:     length,
	})
	roots, ok := c.sectorRoots[contractID]
	if !ok || offset > uint64(len(roots)) {
		return rhp.RPCSectorRootsResult{}, nil
	}
	roots = roots[offset:]
	if length > uint64(len(roots)) {
		return rhp.RPCSectorRootsResult{}, errors.New("out of bounds")
	}
	return rhp.RPCSectorRootsResult{Roots: roots[:length]}, nil
}

func (c *hostClientMock) FreeSectors(ctx context.Context, hostPrices proto.HostPrices, contractID types.FileContractID, indices []uint64) (rhp.RPCFreeSectorsResult, error) {
	c.freeSectorsCalls = append(c.freeSectorsCalls, freeSectorsCall{
		hostPrices: hostPrices,
		contractID: contractID,
		indices:    indices,
	})
	return rhp.RPCFreeSectorsResult{}, nil
}

func TestPerformContractPruningOnHost(t *testing.T) {
	store := newStoreMock()

	// prepare two hosts
	hk1 := types.PublicKey{1}
	h1 := hosts.Host{
		PublicKey: hk1,
		Networks:  []net.IPNet{{IP: net.IP{127, 0, 0, 1}, Mask: net.CIDRMask(24, 32)}},
		Addresses: []chain.NetAddress{{Protocol: siamux.Protocol, Address: "host1.com"}},
		Settings:  goodSettings,
		Usability: hosts.GoodUsability,
	}
	h1.Settings.Prices.StoragePrice = types.NewCurrency64(123)
	h1.Settings.Prices.TipHeight = 111
	store.hosts[hk1] = h1

	hk2 := types.PublicKey{2}
	h2 := hosts.Host{
		PublicKey: hk2,
		Networks:  []net.IPNet{{IP: net.IP{127, 0, 0, 2}, Mask: net.CIDRMask(24, 32)}},
		Addresses: []chain.NetAddress{{Protocol: siamux.Protocol, Address: "host2.com"}},
		Settings:  goodSettings,
		Usability: hosts.GoodUsability,
	}
	h2.Settings.Prices.StoragePrice = types.NewCurrency64(456)
	h2.Settings.Prices.TipHeight = 222
	store.hosts[hk2] = h2

	// add two contracts for h1
	fcid1 := types.FileContractID{1}
	if err := store.AddFormedContract(context.Background(), fcid1, hk1, 100, 200, types.ZeroCurrency, types.NewCurrency64(1), types.ZeroCurrency, types.ZeroCurrency); err != nil {
		t.Fatal(err)
	}
	fcid2 := types.FileContractID{2}
	if err := store.AddFormedContract(context.Background(), fcid2, hk1, 100, 200, types.ZeroCurrency, types.NewCurrency64(1), types.ZeroCurrency, types.ZeroCurrency); err != nil {
		t.Fatal(err)
	}

	// add one contract for h2
	fcid3 := types.FileContractID{3}
	if err := store.AddFormedContract(context.Background(), fcid3, hk2, 100, 200, types.ZeroCurrency, types.NewCurrency64(1), types.ZeroCurrency, types.ZeroCurrency); err != nil {
		t.Fatal(err)
	}

	// prepare roots
	r1 := types.Hash256{1}
	r2 := types.Hash256{2}
	r3 := types.Hash256{3}
	r4 := types.Hash256{4}
	r5 := types.Hash256{5}
	r6 := types.Hash256{6}
	r7 := types.Hash256{7}
	r8 := types.Hash256{8}
	r9 := types.Hash256{9}

	store.sectors[hk1] = []sector{{root: r1, contractID: &fcid1}, {root: r2, contractID: &fcid1}, {root: r4, contractID: &fcid2}, {root: r7, contractID: &fcid2}, {root: r8, contractID: &fcid2}} // r3, r5, r6 dropped
	store.sectors[hk2] = []sector{{root: r9, contractID: &fcid3}}                                                                                                                                 // none dropped                                                                                                                                                // r5, r6 dropped

	// prepare dialer
	h1Mock := newHostClientMock()
	h2Mock := newHostClientMock()

	dialer := newDialerMock()
	dialer.clients[hk1] = h1Mock
	dialer.clients[hk2] = h2Mock

	// prepare roots
	h1Mock.sectorRoots[fcid1] = []types.Hash256{r1, r2, r3}
	h1Mock.sectorRoots[fcid2] = []types.Hash256{r4, r5, r6, r7, r8}
	h2Mock.sectorRoots[fcid3] = []types.Hash256{r9}

	// set contract sizes
	for i, c := range store.contracts {
		switch c.ID {
		case fcid1:
			store.contracts[i].Size = proto.SectorSize * uint64(len(h1Mock.sectorRoots[fcid1]))
		case fcid2:
			store.contracts[i].Size = proto.SectorSize * uint64(len(h1Mock.sectorRoots[fcid2]))
		case fcid3:
			store.contracts[i].Size = proto.SectorSize * uint64(len(h2Mock.sectorRoots[fcid3]))
		}
	}

	// prepare scanner
	scanner := store.Scanner()
	scanner.settings[hk1] = h1.Settings
	scanner.settings[hk2] = h2.Settings

	// prepare contract manager
	cm := newContractManager(types.PublicKey{}, nil, nil, dialer, scanner, store, nil, nil)

	// prune contracts on h1
	err := cm.performContractPruningOnHost(context.Background(), h1, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to perform contract pruning: %v", err)
	}

	// assert rpc calls
	if len(h1Mock.sectorRootsCalls) != 2 {
		t.Fatalf("expected 2 sector roots calls, got %d", len(h1Mock.sectorRootsCalls))
	} else if call := h1Mock.sectorRootsCalls[0]; call.contractID != fcid2 {
		t.Fatalf("expected contract ID %v, got %v", fcid2, call.contractID)
	} else if call.offset != 0 || call.length != 5 {
		t.Fatalf("expected offset 0 and length 5, got offset %d and length %d", call.offset, call.length)
	} else if call = h1Mock.sectorRootsCalls[1]; call.contractID != fcid1 {
		t.Fatalf("expected contract ID %v, got %v", fcid1, call.contractID)
	} else if call.offset != 0 || call.length != 3 {
		t.Fatalf("expected offset 0 and length 3, got offset %d and length %d", call.offset, call.length)
	} else if len(h1Mock.freeSectorsCalls) != 2 {
		t.Fatalf("expected 2 free sectors calls, got %d", len(h1Mock.freeSectorsCalls))
	} else if call := h1Mock.freeSectorsCalls[0]; call.contractID != fcid2 {
		t.Fatalf("expected contract ID %v, got %v", fcid2, call.contractID)
	} else if len(call.indices) != 2 {
		t.Fatalf("expected 2 indices, got %d", len(call.indices))
	} else if call.indices[0] != 1 || call.indices[1] != 2 {
		t.Fatalf("expected indices [1, 2], got %v", call.indices)
	} else if call = h1Mock.freeSectorsCalls[1]; call.contractID != fcid1 {
		t.Fatalf("expected contract ID %v, got %v", fcid1, call.contractID)
	} else if len(call.indices) != 1 {
		t.Fatalf("expected 1 index, got %d", len(call.indices))
	} else if call.indices[0] != 2 {
		t.Fatalf("expected index 2, got %v", call.indices)
	}

	// prune contracts on h2
	err = cm.performContractPruningOnHost(context.Background(), h2, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to perform contract pruning: %v", err)
	}

	// assert rpc calls
	if len(h2Mock.sectorRootsCalls) != 1 {
		t.Fatalf("expected 1 sector roots calls, got %d", len(h2Mock.sectorRootsCalls))
	} else if call := h2Mock.sectorRootsCalls[0]; call.contractID != fcid3 {
		t.Fatalf("expected contract ID %v, got %v", fcid2, call.contractID)
	} else if call.offset != 0 || call.length != 1 {
		t.Fatalf("expected offset 0 and length 1, got offset %d and length %d", call.offset, call.length)
	} else if len(h2Mock.freeSectorsCalls) != 0 {
		t.Fatalf("expected 0 free sectors calls, got %d", len(h2Mock.freeSectorsCalls))
	}

	// assert contracts are marked as pruned
	if contracts, err := store.ContractsForPruning(context.Background(), hk1, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("failed to fetch contracts for pruning: %v", err)
	} else if len(contracts) != 0 {
		t.Fatalf("expected no contracts for pruning, got %v", contracts)
	} else if contracts, err := store.ContractsForPruning(context.Background(), hk2, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("failed to fetch contracts for pruning: %v", err)
	} else if len(contracts) != 0 {
		t.Fatalf("expected no contracts for pruning, got %v", contracts)
	}
}
