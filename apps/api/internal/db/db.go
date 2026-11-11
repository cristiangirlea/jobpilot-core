package db

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	// The planner prices per-job history high enough to start JIT, which costs more than the query.
	cfg.ConnConfig.RuntimeParams["jit"] = "off"
	for i := 0; i < 20; i++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			err = pool.Ping(ctx)
		}
		if err == nil {
			return pool, nil
		}
		if pool != nil {
			pool.Close()
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("connect to database: %w", err)
}
