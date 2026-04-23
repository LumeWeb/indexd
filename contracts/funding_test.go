package contracts_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/rhp/v4/siamux"
	"go.sia.tech/coreutils/testutil"
	"go.sia.tech/indexd/accounts"
	"go.sia.tech/indexd/alerts"
	"go.sia.tech/indexd/contracts"
	"go.sia.tech/indexd/hosts"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type fundingAccountCall struct {
	host        hosts.Host
	accounts    []accounts.HostAccount
	contractIDs []types.FileContractID
	target      types.Currency
}

type mockFunder struct {
	calls    []fundingAccountCall
	fail     bool
	deposits []contracts.FundedDeposit
}

func (f *mockFunder) FundAccounts(ctx context.Context, host hosts.Host, contractIDs []types.FileContractID, accs []accounts.HostAccount, target types.Currency, log *zap.Logger) (funded int, drained int, _ []contracts.FundedDeposit, _ error) {
	f.calls = append(f.calls, fundingAccountCall{
		host:        host,
		accounts:    accs,
		contractIDs: contractIDs,
		target:      target,
	})
	if f.fail {
		return 0, 0, nil, nil
	}
	return len(accs), 1, f.deposits, nil
}

// TestFunding is a unit test that covers the functionality of the
// FundAccounts method on the contracts manager.
func TestFunding(t *testing.T) {
	log := zaptest.NewLogger(t)
	s := newTestStore(t)
	f := &mockFunder{}

	am, err := accounts.NewManager(s, accounts.WithLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	defer am.Close()

	network, genesis := testutil.V2Network()
	dbstore, tipState, err := chain.NewDBStore(chain.NewMemDB(), network, genesis, chain.NewZapMigrationLogger(log.Named("chaindb")))
	if err != nil {
		t.Fatal(err)
	}

	hm, err := hosts.NewManager(nil, nil, nil, s, alerts.NewManager(), hosts.WithLogger(log.Named("hosts")))
	if err != nil {
		t.Fatal(err)
	}
	defer hm.Close()

	cm, err := contracts.NewManager(types.GeneratePrivateKey(), am, f, chain.NewManager(dbstore, tipState), s, nil, nil, nil, contracts.NewContractLocker(), hm, nil, nil, contracts.WithLogger(log.Named("contracts")))
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()

	hs := proto.HostSettings{
		Prices: proto.HostPrices{
			EgressPrice:  types.Siacoins(1),
			IngressPrice: types.Siacoins(1),
			StoragePrice: types.Siacoins(1),
		},
	}

	host := hosts.Host{
		Settings:  hs,
		PublicKey: types.GeneratePrivateKey().PublicKey(),
		Addresses: []chain.NetAddress{{Protocol: siamux.Protocol, Address: "foo"}},
		Usability: hosts.GoodUsability,
	}

	contractIDs := []types.FileContractID{{1}}
	err = cm.FundAccounts(context.Background(), host, contractIDs, false, zap.NewNop())
	if !errors.Is(err, hosts.ErrNotFound) {
		t.Fatal("expected host not found error")
	}

	// add a host and two accounts
	s.AddTestHost(t, host)

	pk1 := types.GeneratePrivateKey().PublicKey()
	s.AddTestAccount(t, pk1)

	pk2 := types.GeneratePrivateKey().PublicKey()
	s.AddTestAccount(t, pk2)

	// fund accounts
	err = cm.FundAccounts(context.Background(), host, contractIDs, false, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	// assert the call params
	if len(f.calls) != 1 {
		t.Fatal("expected one call to fund accounts")
	} else if !reflect.DeepEqual(f.calls[0].host, host) {
		t.Fatal("expected host key to match")
	} else if len(f.calls[0].accounts) != 2 {
		t.Fatal("expected two accounts to be funded")
	}

	// assert the accounts were updated
	eas := s.hostAccounts(t)
	if len(eas) != 2 {
		t.Fatal("expected two accounts to be updated")
	}
	expected := time.Now().Add(accounts.AccountFundInterval)
	for _, ea := range eas {
		if !approxEqual(ea.NextFund, expected) {
			t.Fatal("expected next fund to be updated to the next fund interval", ea.NextFund)
		}
	}

	// simulate a couple of failed fund attempts
	f.fail = true
	for range 3 {
		s.resetNextFund(t)
		err = cm.FundAccounts(context.Background(), host, contractIDs, false, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
	}

	// assert the exponential backoff was applied
	expected = time.Now().Add(8 * time.Minute)
	for _, ea := range s.hostAccounts(t) {
		if !approxEqual(ea.NextFund, expected) {
			t.Fatal("expected next fund to be updated to the exponential backoff", ea.NextFund)
		}
	}

	// reset state
	f.fail = false
	f.calls = f.calls[:0]
	s.resetNextFund(t)

	// add another 1000 accounts
	for range 1000 {
		pk := types.GeneratePrivateKey().PublicKey()
		s.AddTestAccount(t, pk)
	}

	// fund accounts
	contractIDs = append(contractIDs, types.FileContractID{2})
	err = cm.FundAccounts(context.Background(), host, contractIDs, false, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	// assert batches were applied correctly
	target := accounts.HostFundTarget(host, testFundTargetBytes)
	if len(f.calls) != 2 {
		t.Fatal("expected two calls to fund accounts")
	} else if len(f.calls[0].accounts) != accounts.AccountFundBatch {
		t.Fatal("expected first call to fund 1000 accounts")
	} else if len(f.calls[1].accounts) != 2 {
		t.Fatal("expected second call to fund 2 accounts")
	} else if len(f.calls[0].contractIDs) != 2 {
		t.Fatal("expected first call to have two contract IDs")
	} else if len(f.calls[1].contractIDs) != 1 {
		t.Fatal("expected second call to have one contract ID")
	} else if !f.calls[0].target.Equals(target) {
		t.Fatalf("expected target to be %v, got %v", target, f.calls[0].target)
	} else if !f.calls[1].target.Equals(target) {
		t.Fatalf("expected target to be %v, got %v", target, f.calls[1].target)
	}

	// assert all accounts next fund was updated and consecutive failed funds was reset
	expected = time.Now().Add(accounts.AccountFundInterval)
	for _, ea := range s.hostAccounts(t) {
		if !approxEqual(ea.NextFund, expected) {
			t.Fatal("expected next fund to be updated to the next fund interval", ea.NextFund)
		}
	}

	// assert there's no accounts to fund
	err = cm.FundAccounts(context.Background(), host, contractIDs, false, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	} else if len(f.calls) != 2 {
		t.Fatal("expected two calls to fund accounts")
	}

	// assert we can force a refill on all accounts
	err = cm.FundAccounts(context.Background(), host, contractIDs, true, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	} else if len(f.calls) != 4 {
		t.Fatal("expected four calls to fund accounts")
	}
}

// approxEqual checks if two time.Time values are within a second of each
// other.
func approxEqual(t1, t2 time.Time) bool {
	const tol = time.Second

	diff := t1.Sub(t2)
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}

func TestFundAccountsRecordsFundingEvents(t *testing.T) {
	log := zaptest.NewLogger(t)
	s := newTestStore(t)

	hk := types.GeneratePrivateKey().PublicKey()
	host := hosts.Host{
		PublicKey: hk,
		Settings: proto.HostSettings{
			Prices: proto.HostPrices{
				EgressPrice:  types.NewCurrency64(100),
				IngressPrice: types.NewCurrency64(100),
				StoragePrice: types.NewCurrency64(100),
			},
		},
		Addresses: []chain.NetAddress{{Protocol: siamux.Protocol, Address: "foo"}},
		Usability: hosts.GoodUsability,
	}

	s.addTestHost(t, host)

	pk := types.GeneratePrivateKey()
	s.AddTestAccount(t, pk.PublicKey())

	fcid := types.FileContractID{1}
	s.addTestContract(t, hk, true, fcid)
	s.setContractRemainingAllowance(t, fcid, types.Siacoins(10000))

	ak := proto.Account(pk.PublicKey())
	depositAmount := types.Siacoins(50)
	f := &mockFunder{
		deposits: []contracts.FundedDeposit{
			{
				ContractID: fcid,
				Deposit: proto.AccountDeposit{
					Account: ak,
					Amount:  depositAmount,
				},
			},
		},
	}

	am, _ := accounts.NewManager(s, accounts.WithLogger(log))
	defer am.Close()
	network, genesis := testutil.V2Network()
	dbstore, tipState, _ := chain.NewDBStore(chain.NewMemDB(), network, genesis, nil)
	hm, _ := hosts.NewManager(nil, nil, nil, s, alerts.NewManager(), hosts.WithLogger(log.Named("hosts")))
	defer hm.Close()
	cm, _ := contracts.NewManager(types.GeneratePrivateKey(), am, f, chain.NewManager(dbstore, tipState), s, nil, nil, nil, contracts.NewContractLocker(), hm, nil, nil, contracts.WithLogger(log.Named("contracts")))
	defer cm.Close()

	s.resetNextFund(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cm.FundAccounts(ctx, host, []types.FileContractID{fcid}, true, log); err != nil {
		t.Fatalf("failed to fund accounts: %v", err)
	}

	events, err := s.FundingEvents(accounts.FundingCursor{}, 100)
	if err != nil {
		t.Fatalf("failed to get funding events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected funding events to be recorded")
	}

	found := false
	for _, ev := range events {
		if ev.AccountKey == ak && ev.AmountSC.Cmp(depositAmount) == 0 {
			found = true
			if ev.ContractID != fcid {
				t.Fatalf("expected contract ID %x, got %x", fcid, ev.ContractID)
			}
			if ev.EstimatedUploadBytes == 0 {
				t.Fatal("expected non-zero EstimatedUploadBytes")
			}
			if ev.EstimatedDownloadBytes == 0 {
				t.Fatal("expected non-zero EstimatedDownloadBytes")
			}
			break
		}
	}
	if !found {
		t.Fatal("expected to find funding event matching deposit")
	}
}
