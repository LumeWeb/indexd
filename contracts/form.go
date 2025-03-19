package contracts

import (
	"context"
	"fmt"

	proto "go.sia.tech/core/rhp/v4"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/rhp/v4"
	"go.sia.tech/coreutils/rhp/v4/siamux"
	"go.sia.tech/indexd/hosts"
	"go.uber.org/zap"
	"lukechampine.com/frand"
)

type formContractSigner struct {
	renterKey types.PrivateKey
	w         rhp.Wallet
}

func NewFormContractSigner(w rhp.Wallet, renterKey types.PrivateKey) rhp.FormContractSigner {
	return &formContractSigner{
		renterKey: renterKey,
		w:         w,
	}
}

func (s *formContractSigner) FundV2Transaction(txn *types.V2Transaction, amount types.Currency) (types.ChainIndex, []int, error) {
	return s.w.FundV2Transaction(txn, amount, true)
}

func (s *formContractSigner) ReleaseInputs(txns []types.V2Transaction) {
	s.w.ReleaseInputs(nil, txns)
}

func (s *formContractSigner) SignHash(h types.Hash256) types.Signature {
	return s.renterKey.SignHash(h)
}

func (s *formContractSigner) SignV2Inputs(txn *types.V2Transaction, toSign []int) {
	s.w.SignV2Inputs(txn, toSign)
}

type contractFormer struct {
	cm     *chain.Manager
	signer *formContractSigner
}

// NewContractFormer creates a production ContractFormer that forms contracts by
// dialing up hosts using the SiaMux protocol and fetching fresh settings right
// before forming the contract.
func NewContractFormer(cm *chain.Manager, w rhp.Wallet, renterKey types.PrivateKey) ContractFormer {
	return &contractFormer{
		cm: cm,
		signer: &formContractSigner{
			renterKey: renterKey,
			w:         w,
		},
	}
}

func (cf *contractFormer) FormContract(ctx context.Context, hk types.PublicKey, addr string, params proto.RPCFormContractParams) (rhp.RPCFormContractResult, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	t, err := siamux.Dial(dialCtx, addr, hk)
	if err != nil {
		return rhp.RPCFormContractResult{}, fmt.Errorf("failed to dial host: %w", err)
	}
	defer t.Close()

	// fetch fresh settings to make sure they are valid
	settings, err := rhp.RPCSettings(ctx, t)
	if err != nil {
		return rhp.RPCFormContractResult{}, fmt.Errorf("failed to fetch host settings: %w", err)
	}

	// forming a contract only requires the ContractPrice field of the settings
	// so we make sure it's sane
	if settings.Prices.ContractPrice.Cmp(types.Siacoins(1)) > 0 {
		return rhp.RPCFormContractResult{}, fmt.Errorf("host's contract price is too high: %v", settings.Prices.ContractPrice)
	}

	res, err := rhp.RPCFormContract(ctx, t, cf.cm, cf.signer, cf.cm.TipState(), settings.Prices, hk, settings.WalletAddress, params)
	if err != nil {
		return rhp.RPCFormContractResult{}, fmt.Errorf("failed to form contract: %w", err)
	}

	return res, nil
}

func (cm *ContractManager) performContractFormation(ctx context.Context, wanted uint, log *zap.Logger) error {
	formationLog := log.Named("formation")
	activeContracts, err := cm.store.Contracts(ctx, WithRevisable(true))
	if err != nil {
		return fmt.Errorf("failed to fetch active contracts: %w", err)
	}

	// helper to check if a host is good to form a contract with
	usedCidrs := make(map[string]struct{})
	checkHost := func(host hosts.Host, log *zap.Logger) bool {
		if good := true; !good { // TODO: update
			// host should be good
			log.Debug("ignore contract since host is not good", zap.Stringer("hostKey", host.PublicKey))
			return false
		} else if _, used := usedCidrs[""]; used { // TODO: update
			// host should be on a unique cidr
			log.Debug("ignore contract since host's cidr has already been used", zap.Stringer("hostKey", host.PublicKey))
			return false
		} else if host.Settings.RemainingStorage < minRemainingStorage {
			// host should at least have 10GB of storage left
			log.Debug("ignore contract since host has less than 1GB of storage left", zap.Stringer("hostKey", host.PublicKey), zap.Uint64("remainingStorage", host.Settings.RemainingStorage))
			return false
		}
		return true
	}

	// determine how many contracts we need to form
	for _, contract := range activeContracts {
		contractLog := formationLog.Named(contract.ID.String()).With(zap.Stringer("hostKey", contract.HostKey))

		// host checks
		host, err := cm.store.Host(ctx, contract.HostKey)
		if err != nil {
			contractLog.Error("failed to fetch host for contract", zap.Error(err))
			continue
		} else if !checkHost(host, contractLog) {
			continue
		}

		// contract checks
		if !contract.Good {
			// contract should be good
			log.Debug("skipping contract since it's not good")
			continue
		} else if contract.Size >= maxContractSize {
			// contracts should be smaller than 10TB
			log.Debug("skipping contract since it is too large", zap.Uint64("size", contract.Size))
			continue
		} else if contract.UsedCollateral.Cmp(host.Settings.MaxCollateral) > 0 {
			// host should be willing to put more collateral into the contract
			contractLog.Debug("ignore contract since the host won't put more collateral into it", zap.Stringer("maxCollateral", host.Settings.MaxCollateral), zap.Stringer("usedCollateral", contract.UsedCollateral))
			continue
		}

		// contract is good
		wanted--
		// TODO: add cidr
	}

	// fetch all hosts
	hosts, err := cm.store.Hosts(ctx, 0, -1)
	if err != nil {
		return fmt.Errorf("failed to fetch hosts to form contracts with: %w", err)
	}

	// randomize their order to avoid prefering any host
	frand.Shuffle(len(hosts), func(i, j int) { hosts[i], hosts[j] = hosts[j], hosts[i] })

	for _, candidate := range hosts {
		formationLog := formationLog.With(zap.Stringer("hostKey", candidate.PublicKey))
		if !checkHost(candidate, formationLog) {
			continue // ignore host
		}

		// TODO: form contracts

		wanted--
		// TODO: add cidr
	}

	return nil
}
