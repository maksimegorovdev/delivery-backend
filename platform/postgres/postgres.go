package postgres

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultMaxConns = 10
)

type postgres struct {
	maxConns int32
}

func New(ctx context.Context, dsn string, opts ...Option) (*pgxpool.Pool, error) {
	pg := &postgres{
		maxConns: defaultMaxConns,
	}

	for _, opt := range opts {
		opt(pg)
	}

	poolCfg, err := newPoolConfig(dsn, pg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}

	if pingErr := pool.Ping(ctx); pingErr != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", pingErr)
	}

	if statsErr := otelpgx.RecordStats(pool); statsErr != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: record stats: %w", statsErr)
	}

	return pool, nil
}

// newPoolConfig не включает аргументы запроса в спаны (otelpgx.WithIncludeQueryParameters),
// чтобы значения параметров не попадали в трейсы.
func newPoolConfig(dsn string, pg *postgres) (*pgxpool.Config, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}

	poolCfg.MaxConns = pg.maxConns
	poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	return poolCfg, nil
}
