package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.sia.tech/core/types"
	"go.sia.tech/indexd/api/app"
)

// AddAppConnectKey adds or updates an application connection key in the database.
func (s *Store) AddAppConnectKey(ctx context.Context, key app.AddConnectKey) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO app_connect_keys (app_key, use_description, remaining_uses) VALUES ($1, $2, $3)
			ON CONFLICT (app_key) DO UPDATE SET (use_description, remaining_uses) = (EXCLUDED.use_description, EXCLUDED.remaining_uses);
		`, key.Key, key.Description, key.RemainingUses)
		return err
	})
}

// ValidAppConnectKey checks if an application connection key is valid.
func (s *Store) ValidAppConnectKey(ctx context.Context, key string) (bool, error) {
	var count int
	err := s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		return tx.QueryRow(ctx, `
			SELECT remaining_uses FROM app_connect_keys WHERE app_key = $1 AND remaining_uses > 0
		`, key).Scan(&count)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return count > 0, nil
}

// AppConnectKeys retrieves a list of application connection keys from the database.
func (s *Store) AppConnectKeys(ctx context.Context, offset, limit int) (keys []app.ConnectKey, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		rows, err := tx.Query(ctx, `
			SELECT app_key, use_description, remaining_uses, date_created 
			FROM app_connect_keys
			ORDER BY date_created DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var key app.ConnectKey
			if err := rows.Scan(&key.Key, &key.Description, &key.RemainingUses, &key.DateCreated); err != nil {
				return err
			}
			keys = append(keys, key)
		}
		return rows.Err()
	})
	return
}

// DeleteAppConnectKey deletes an application connection key from the database.
func (s *Store) DeleteAppConnectKey(ctx context.Context, connectKey string) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		_, err := tx.Exec(ctx, `
			DELETE FROM app_connect_keys WHERE app_key = $1
		`, connectKey)
		return err
	})
}

// UseAppConnectKey decrements the remaining uses of a connect key
// and adds the app account.
func (s *Store) UseAppConnectKey(ctx context.Context, connectKey string, appKey types.PublicKey) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		if err := addAccount(ctx, tx, appKey); err != nil {
			return fmt.Errorf("failed to add app account: %w", err)
		}

		res, err := tx.Exec(ctx, `
			UPDATE app_connect_keys SET remaining_uses = remaining_uses - 1
			WHERE app_key = $1 AND remaining_uses > 0
		`, connectKey)
		if err != nil {
			return fmt.Errorf("failed to update app connect key %q: %w", connectKey, err)
		} else if res.RowsAffected() != 1 {
			return fmt.Errorf("failed to use app connect key %q", connectKey)
		}
		return nil
	})
}
