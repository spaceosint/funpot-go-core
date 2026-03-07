package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/funpot/funpot-go-core/internal/config"
)

// OpenPostgresPool creates and validates a PostgreSQL connection pool.
func OpenPostgresPool(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxOpenConns)
	poolCfg.MinConns = int32(cfg.MinOpenConns)
	poolCfg.MaxConnIdleTime = 10 * time.Minute
	poolCfg.HealthCheckPeriod = 30 * time.Second
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.HealthcheckPing)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}
