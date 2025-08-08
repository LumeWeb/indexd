package postgres

import (
	"context"

	"go.uber.org/zap"
)

var migrations = []func(context.Context, *txn, *zap.Logger) error{
	// adds the app_connect_keys table
	func(ctx context.Context, tx *txn, _ *zap.Logger) error {
		_, err := tx.Exec(ctx, `CREATE TABLE app_connect_keys (
    app_key TEXT PRIMARY KEY,
    use_description TEXT NOT NULL,
    remaining_uses INTEGER NOT NULL,
    date_created TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);`)
		return err
	},
}
