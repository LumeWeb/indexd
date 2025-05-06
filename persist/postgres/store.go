package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.sia.tech/coreutils/threadgroup"
	"go.uber.org/zap"
	"lukechampine.com/frand"
)

type (
	// ConnectionInfo contains the information needed to connect to a PostgreSQL
	// database.
	ConnectionInfo struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Database string `json:"database"`
		SSLMode  string `json:"sslmode"`
	}

	// A Store is a persistent store that uses a SQL database as its backend.
	Store struct {
		pool *pgxpool.Pool
		log  *zap.Logger
		tg   *threadgroup.ThreadGroup
	}
)

// String returns a connection string for the given ConnectionInfo.
func (ci ConnectionInfo) String() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s", ci.Host, ci.Port, ci.User, ci.Password, ci.Database, ci.SSLMode)
}

func (s *Store) transaction(ctx context.Context, fn func(context.Context, *txn) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	log := s.log.Named("transaction").With(zap.String("id", hex.EncodeToString(frand.Bytes(4))))
	if err := fn(ctx, &txn{tx, log}); err != nil {
		return err
	} else if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	s.tg.Stop()
	s.pool.Close()
	return nil
}

// Connect connects to a running PostgresSQL server. The passed in context
// determines the lifecycle of necessary migrations. If the context is cancelled,
// the running migration will be interupted and an error returned.
func Connect(ctx context.Context, ci ConnectionInfo, log *zap.Logger) (*Store, error) {
	pool, err := pgxpool.New(ctx, ci.String())
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	store := &Store{
		pool: pool,
		log:  log,
		tg:   threadgroup.New(),
	}
	if err := store.init(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}

	ctx, cancel, err := store.tg.AddContext(context.Background())
	if err != nil {
		return nil, err
	}
	go func() {
		defer cancel()

		refreshTicker := time.NewTicker(time.Hour)
		defer refreshTicker.Stop()

		for {
			select {
			case <-refreshTicker.C:
			case <-ctx.Done():
				return
			}

			if err := store.transaction(ctx, func(ctx context.Context, tx *txn) error {
				_, err := tx.Exec(ctx, `REFRESH MATERIALIZED VIEW CONCURRENTLY unhealthy_slabs`)
				return err
			}); err != nil && !errors.Is(err, context.Canceled) {
				store.log.Warn("failed to refresh materialized view", zap.Error(err))
			}
		}
	}()

	return store, nil
}
