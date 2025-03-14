package contracts

import (
	"context"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/threadgroup"
	"go.uber.org/zap"
)

type (
	// ChainManager is the minimal interface of ChainManager functionality the
	// ContractManager requires.
	ChainManager interface {
		AddV2PoolTransactions(basis types.ChainIndex, txns []types.V2Transaction) (known bool, err error)
		RecommendedFee() types.Currency
	}

	// Store is the minimal interface of Store functionality the ContractManager
	// requires.
	Store interface {
		ContractElementsForBroadcast(ctx context.Context, maxBlocksSinceExpiry uint64) ([]types.V2FileContractElement, error)
		RejectPendingContracts(ctx context.Context, maxFormation time.Time) error
		PruneExpiredContractElements(ctx context.Context, maxBlocksSinceExpiry uint64) error
	}

	// Syncer is the minimal interface of Syncer functionality the
	// ContractManager requires.
	Syncer interface {
		BroadcastV2TransactionSet(index types.ChainIndex, txns []types.V2Transaction)
	}

	// Wallet is the minimal interface of Wallet functionality the
	// ContractManager requires.
	Wallet interface {
		FundV2Transaction(txn *types.V2Transaction, amount types.Currency, useUnconfirmed bool) (types.ChainIndex, []int, error)
		ReleaseInputs(txns []types.Transaction, v2txns []types.V2Transaction)
		SignV2Inputs(txn *types.V2Transaction, toSign []int)
	}
)

type (
	// ContractManagerOpt is a functional option for the ContractManager.
	ContractManagerOpt func(*ContractManager)

	// ContractManager manages the host announcements.
	ContractManager struct {
		cm    ChainManager
		s     Syncer
		w     Wallet
		store Store

		log *zap.Logger
		tg  *threadgroup.ThreadGroup

		contractRejectBuffer           time.Duration
		expiredContractBroadcastBuffer uint64
		expiredContractPruneBuffer     uint64
		maintenanceFrequency           time.Duration
	}
)

// WithLogger creates the contract manager with a custom logger
func WithLogger(l *zap.Logger) ContractManagerOpt {
	return func(cm *ContractManager) {
		cm.log = l
	}
}

// NewManager creates a new contract manager. It is responsible for forming and
// renewing contracts as well as any interactions with hosts that require
// contracts.
func NewManager(chainManager ChainManager, store Store, syncer Syncer, wallet Wallet, opts ...ContractManagerOpt) (*ContractManager, error) {
	cm := &ContractManager{
		cm: chainManager,
		s:  syncer,
		w:  wallet,

		store: store,

		log: zap.NewNop(),
		tg:  threadgroup.New(),

		contractRejectBuffer:           6 * time.Hour, // 6 hours after formation
		expiredContractBroadcastBuffer: 144,           // 144 block after expiration
		expiredContractPruneBuffer:     144,           // 144 blocks after broadcast
		maintenanceFrequency:           10 * time.Minute,
	}
	for _, opt := range opts {
		opt(cm)
	}
	return cm, nil
}

// Close closes the contract manager, terminates any background tasks and waits
// for them to exit.
func (cm *ContractManager) Close() error {
	cm.tg.Stop()
	return nil
}

// Run starts the contract manager's background tasks.
func (cm *ContractManager) Run() {
	done, err := cm.tg.Add()
	if err != nil {
		return
	}
	defer done()

	log := cm.log.Named("maintenance")
	for {
		select {
		case <-cm.tg.Done():
			return
		case <-time.After(cm.maintenanceFrequency):
		}

		if err := cm.performContractMaintenance(); err != nil {
			log.Error("contract maintenance failed", zap.Error(err))
		}

		// TODO: use account manager to fund accounts using the good contracts

		if err := cm.performSlabPinning(); err != nil {
			log.Error("slab pinning failed", zap.Error(err))
		}

		if err := cm.performContractPruning(); err != nil {
			log.Error("contract pruning failed", zap.Error(err))
		}
	}
}

func (cm *ContractManager) performContractMaintenance() error {
	// TODO: fetch settings for maintenance from the store

	// TODO: Use host manager to perform host checks and update results in the store

	// TODO: Mark hosts as well as their contracts as bad if they fail the checks

	// TODO: Renew any good contracts within their renew window

	// TODO: Refresh any good contracts that are either out of collateral or funds

	// TODO: Mark any contracts that failed to renew/refresh and are too close
	// to the expiration height as bad

	// TODO: Form enough contracts to meet the desired number of usable
	// contracts

	return nil
}

// TODO: implement
func (cm *ContractManager) performContractPruning() error {
	return nil
}

// TODO: implement
func (cm *ContractManager) performSlabPinning() error {
	return nil
}
