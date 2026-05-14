package accounts_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"go.sia.tech/indexd/accounts"
	"go.sia.tech/indexd/contracts"
	"go.sia.tech/indexd/testutils"
	"go.uber.org/zap/zaptest"
)

type testStore struct {
	testutils.TestStore
}

func newTestStore(t testing.TB) testStore {
	s := testutils.NewDB(t, contracts.DefaultMaintenanceSettings, zaptest.NewLogger(t))
	t.Cleanup(func() {
		s.Close()
	})

	return testStore{s}
}

// TestUpdateFundedPools is a unit test that covers the functionality of
// updating the funded pool. It asserts that the consecutive failed funds
// and next fund time are updated correctly based on the number of funded
// pools.
func TestUpdateFundedPools(t *testing.T) {
	maxBackoff := 128 * time.Minute
	tests := []struct {
		name   string
		pools  []accounts.HostPool
		funded int
		panic  bool
	}{
		{
			name: "all funded",
			pools: []accounts.HostPool{
				{ConsecutiveFailedFunds: 3},
				{ConsecutiveFailedFunds: 5},
			},
			funded: 2,
		},
		{
			name: "none funded",
			pools: []accounts.HostPool{
				{ConsecutiveFailedFunds: 0},
				{ConsecutiveFailedFunds: 1},
			},
			funded: 0,
		},
		{
			name: "partially funded",
			pools: []accounts.HostPool{
				{ConsecutiveFailedFunds: 2},
				{ConsecutiveFailedFunds: 4},
				{ConsecutiveFailedFunds: 0},
			},
			funded: 2,
		},
		{
			name: "sanity check",
			pools: []accounts.HostPool{
				{ConsecutiveFailedFunds: 1},
			},
			funded: 2,
			panic:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.panic {
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("expected panic but function did not panic")
					}
				}()
				accounts.UpdateFundedPools(tc.pools, tc.funded, maxBackoff)
				return
			}

			updated := slices.Clone(tc.pools)
			accounts.UpdateFundedPools(updated, tc.funded, maxBackoff)

			for i, pool := range updated {
				var wantConsecFailures int
				var wantNextFund time.Time
				if i < tc.funded {
					wantConsecFailures = 0
					wantNextFund = time.Now().Add(accounts.PoolFundInterval)
				} else {
					wantConsecFailures = tc.pools[i].ConsecutiveFailedFunds + 1
					wantNextFund = time.Now().Add(min(time.Duration(math.Pow(2, float64(wantConsecFailures)))*time.Minute, maxBackoff))
				}

				if pool.ConsecutiveFailedFunds != wantConsecFailures {
					t.Fatal("unexpected consecutive failed funds", pool.ConsecutiveFailedFunds, wantConsecFailures)
				} else if !approxEqual(pool.NextFund, wantNextFund) {
					t.Fatal("unexpected next fund", pool.NextFund, wantNextFund)
				}
			}
		})
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
