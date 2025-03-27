package postgres

import (
	"context"
	"errors"
	"fmt"

	"go.sia.tech/core/types"
	"go.sia.tech/indexd/accounts"
)

// Accounts returns a list of account keys.
func (s *Store) Accounts(ctx context.Context, offset, limit int) ([]types.PublicKey, error) {
	if err := validateOffsetLimit(offset, limit); err != nil {
		return nil, err
	} else if limit == 0 {
		return nil, nil
	}

	var accs []types.PublicKey
	if err := s.transaction(ctx, func(ctx context.Context, tx *txn) (err error) {
		rows, err := tx.Query(ctx, `SELECT public_key FROM accounts LIMIT $1 OFFSET $2`, limit, offset)
		if err != nil {
			return fmt.Errorf("failed to query accounts: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var ak types.PublicKey
			if err := rows.Scan((*sqlPublicKey)(&ak)); err != nil {
				return fmt.Errorf("failed to scan account key: %w", err)
			}
			accs = append(accs, ak)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}

	return accs, nil
}

// AccountsForFunding returns up to limit accounts for the given host key that
// are due for funding.
func (s *Store) AccountsForFunding(ctx context.Context, hk types.PublicKey, limit int) ([]accounts.Account, error) {
	if limit < 0 {
		return nil, errors.New("limit can not be negative")
	} else if limit == 0 {
		return nil, nil
	}

	var accs []accounts.Account
	if err := s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		_, err := tx.Exec(ctx, `
INSERT INTO account_hosts (account_id, host_id)
SELECT a.id, h.id
FROM accounts a
CROSS JOIN hosts h 
WHERE h.public_key = $1
ON CONFLICT DO NOTHING;`, sqlPublicKey(hk))
		if err != nil {
			return fmt.Errorf("failed to ensure accounts: %w", err)
		}

		rows, err := tx.Query(ctx, `
SELECT a.public_key, h.public_key, ea.consecutive_failed_funds, ea.next_fund
FROM account_hosts ea
INNER JOIN hosts h ON h.id = ea.host_id
INNER JOIN accounts a ON a.id = ea.account_id
WHERE h.public_key = $1 AND next_fund <= NOW() 
ORDER BY next_fund ASC
LIMIT $2`, sqlPublicKey(hk), limit)
		if err != nil {
			return fmt.Errorf("failed to query accounts for funding: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var acc accounts.Account
			if err := rows.Scan((*sqlPublicKey)(&acc.AccountKey), (*sqlPublicKey)(&acc.HostKey), &acc.ConsecutiveFailedFunds, &acc.NextFund); err != nil {
				return err
			}
			accs = append(accs, acc)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}

	return accs, nil
}

// AddAccount adds a new account in the database with given account key.
func (s *Store) AddAccount(ctx context.Context, ak types.PublicKey) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		res, err := tx.Exec(ctx, `INSERT INTO accounts (public_key) VALUES ($1) ON CONFLICT DO NOTHING`, sqlPublicKey(ak))
		if err != nil {
			return fmt.Errorf("failed to add account: %w", err)
		} else if res.RowsAffected() == 0 {
			return accounts.ErrExists
		}
		return nil
	})
}

// UpdateAccounts updates the given host accounts in the database.
func (s *Store) UpdateAccounts(ctx context.Context, accounts []accounts.Account) error {
	return s.transaction(ctx, func(ctx context.Context, tx *txn) error {
		for _, account := range accounts {
			_, err := tx.Exec(ctx, `
UPDATE account_hosts ea
SET consecutive_failed_funds = $1, next_fund = $2
FROM accounts a, hosts h
WHERE ea.host_id = h.id AND ea.account_id = a.id AND a.public_key = $3 AND h.public_key = $4
`, account.ConsecutiveFailedFunds, account.NextFund, sqlPublicKey(account.AccountKey), sqlPublicKey(account.HostKey))
			return err
		}
		return nil
	})
}
