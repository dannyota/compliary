// Package db wires the PostgreSQL connection pool for compliary. It builds a
// pgxpool.Pool from the typed database config and registers the pgvector types
// on every connection so vector/sparsevec columns round-trip through pgx.
package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"danny.vn/compliary/pkg/base/config"
)

// NewPool creates a pgxpool.Pool from the database config DSN. AfterConnect
// registers the pgvector types (vector / halfvec / sparsevec) on each new
// connection, which pgvector needs because the extension's type OIDs are
// database-specific and not known to pgx until they are looked up per
// connection.
func NewPool(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	dsn := cfg.DSN()
	if dsn == "" {
		return nil, errors.New("database not configured")
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
			return fmt.Errorf("register pgvector types: %w", err)
		}
		return nil
	}

	// Bound the connect wait, reap idle connections, rotate and health-check.
	// MaxConns is env-tunable (COMPLIARY_DATABASE_MAX_CONNS) — small for the
	// server, larger for the bulk pipeline — otherwise pgx's default
	// (max(4, NumCPU)) applies.
	poolCfg.ConnConfig.ConnectTimeout = 10 * time.Second
	poolCfg.MaxConnIdleTime = time.Minute
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = 30 * time.Second
	if v := os.Getenv("COMPLIARY_DATABASE_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			poolCfg.MaxConns = int32(n)
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	// Bound the startup ping (callers like cmd/seed pass context.Background()).
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}
