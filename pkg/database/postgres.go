package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresSettings holds configuration for establishing PostgreSQL connections.
type PostgresSettings struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	PingTimeout     time.Duration
}

// OpenPostgres establishes a PostgreSQL connection pool using the provided settings.
func OpenPostgres(settings PostgresSettings) (*sql.DB, error) {
	if settings.DSN == "" {
		return nil, errors.New("postgres DSN is required")
	}

	db, err := sql.Open("pgx", settings.DSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	if settings.MaxOpenConns > 0 {
		db.SetMaxOpenConns(settings.MaxOpenConns)
	}
	if settings.MaxIdleConns >= 0 {
		db.SetMaxIdleConns(settings.MaxIdleConns)
	}
	if settings.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(settings.ConnMaxIdleTime)
	}
	if settings.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(settings.ConnMaxLifetime)
	}

	pingTimeout := settings.PingTimeout
	if pingTimeout <= 0 {
		pingTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}
