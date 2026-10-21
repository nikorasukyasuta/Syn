package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool initializes, configures, and verifies a PostgreSQL connection pool with connection retry resilience.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database config: %w", err)
	}

	cfg.MaxConns = 25
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 1 * time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating database pool: %w", err)
	}

	var pingErr error
	backoff := 500 * time.Millisecond
	for {
		pingErr = pool.Ping(ctx)
		if pingErr == nil {
			return pool, nil
		}

		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("pinging database timed out (%w): %v", ctx.Err(), pingErr)
		case <-time.After(backoff):
			if backoff < 3*time.Second {
				backoff *= 2
			}
		}
	}
}
