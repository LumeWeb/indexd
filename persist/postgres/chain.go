package postgres

import (
	"context"

	"go.sia.tech/coreutils/wallet"
	"go.uber.org/zap"
)

// UpdateTx defines the interface for updating the state when processing chain
// updates.
type UpdateTx interface {
	wallet.UpdateTx
}

type updateTx struct {
	ctx context.Context
	tx  *txn
	log *zap.Logger
}

// ApplyChainUpdate applies a chain update to the store.
func (s *Store) ApplyChainUpdate(ctx context.Context, fn func(tx UpdateTx) error) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		return fn(&updateTx{ctx: ctx, tx: tx, log: s.log.Named("UpdateTx")})
	})
}
