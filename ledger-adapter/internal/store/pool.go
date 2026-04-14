package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool создаёт pgxpool с sane defaults + ping на старте.
// Ping timeout 5s, чтобы приложение упало сразу при недоступной БД, а не
// ждало первый запрос.
func NewPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 30 * time.Second

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}

	// 10s — чтобы на медленных CI-раннерах и свежезапущенных постгрес-
	// контейнерах не ловить flaky ping deadline. Если БД реально down —
	// 10s всё равно достаточно быстрый fail-fast.
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.Ping(pingCtx); err != nil {
		p.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return p, nil
}
