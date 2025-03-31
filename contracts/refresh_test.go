package contracts

import (
	"context"
	"testing"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/rhp/v4"
	"lukechampine.com/frand"
)

type refreshContractCall struct {
	hk       types.PublicKey
	addr     string
	settings proto.HostSettings
	params   proto.RPCRefreshContractParams
}

func (c *contractorMock) RefreshContract(ctx context.Context, hk types.PublicKey, addr string, settings proto.HostSettings, params proto.RPCRefreshContractParams) (rhp.RPCRefreshContractResult, error) {
	c.refreshCalls = append(c.refreshCalls, refreshContractCall{
		hk:       hk,
		addr:     addr,
		settings: settings,
		params:   params,
	})
	return rhp.RPCRefreshContractResult{
		Contract: rhp.ContractRevision{
			ID: frand.Entropy256(),
			Revision: types.V2FileContract{
				// NOTE: not quite correct since it doesn't take into account
				// the existing allowance and collateral of the contract but we
				// just want to make sure that some value is returned and stored
				// in the store mock during testing.
				RenterOutput: types.SiacoinOutput{
					Value: params.Allowance,
				},
				TotalCollateral: params.Collateral,
			},
		},
		RenewalSet: rhp.TransactionSet{
			Transactions: []types.V2Transaction{
				{
					MinerFee: types.Siacoins(1),
				},
			},
		},
	}, nil
}

func TestPerformContractRefreshes(t *testing.T) {
	// TODO: implement
}
