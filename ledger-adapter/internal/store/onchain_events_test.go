//go:build integration

// Integration-тесты требуют Docker (testcontainers). Запускать локально:
//   go test -tags integration ./internal/store/... -race -timeout 120s
// В CI требует DinD service — добавить отдельным job'ом.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) *Repository {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:17",
		postgres.WithDatabase("test"),
		postgres.WithUsername("user"),
		postgres.WithPassword("pw"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := NewPool(ctx, dsn)
	require.NoError(t, err, "NewPool")
	t.Cleanup(func() { pool.Close() })

	// Применить схему onchain_events (минимальный срез wms/migrations/0001).
	_, err = pool.Exec(ctx, `
		CREATE TYPE onchain_event_status AS ENUM ('PENDING','SENT','COMMITTED','FAILED');
		CREATE TABLE public.onchain_events (
			id bigserial PRIMARY KEY,
			event_id uuid NOT NULL UNIQUE,
			aggregate_type text NOT NULL,
			tx_hash text,
			status onchain_event_status NOT NULL DEFAULT 'PENDING',
			error_message text,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		);
	`)
	require.NoError(t, err, "create schema")
	return NewRepository(pool)
}

func TestRepository_InsertPending_Exists(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()
	eid := uuid.New()

	// До insert — не существует
	ok, err := repo.Exists(ctx, eid)
	require.NoError(t, err)
	assert.False(t, ok, "не должен существовать до insert")

	require.NoError(t, repo.InsertPending(ctx, eid, "receiving"))

	ok, err = repo.Exists(ctx, eid)
	require.NoError(t, err)
	assert.True(t, ok, "должен существовать после insert")

	// Status = PENDING
	st, err := repo.Status(ctx, eid)
	require.NoError(t, err)
	assert.Equal(t, "PENDING", st)
}

func TestRepository_InsertPending_Idempotent(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()
	eid := uuid.New()

	require.NoError(t, repo.InsertPending(ctx, eid, "receiving"))
	// Повторный insert под тем же event_id — не должен падать (ON CONFLICT DO NOTHING)
	require.NoError(t, repo.InsertPending(ctx, eid, "putaway"))

	// Тип сохраняется от первого insert'а
	st, err := repo.Status(ctx, eid)
	require.NoError(t, err)
	assert.Equal(t, "PENDING", st)
}

func TestRepository_MarkSent_MarkCommitted(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()
	e1, e2 := uuid.New(), uuid.New()

	require.NoError(t, repo.InsertPending(ctx, e1, "receiving"))
	require.NoError(t, repo.InsertPending(ctx, e2, "receiving"))

	require.NoError(t, repo.MarkSent(ctx, []uuid.UUID{e1, e2}, "0xabcdef"))
	for _, eid := range []uuid.UUID{e1, e2} {
		st, err := repo.Status(ctx, eid)
		require.NoError(t, err)
		assert.Equal(t, "SENT", st, "event %s", eid)
	}

	require.NoError(t, repo.MarkCommitted(ctx, []uuid.UUID{e1, e2}))
	for _, eid := range []uuid.UUID{e1, e2} {
		st, err := repo.Status(ctx, eid)
		require.NoError(t, err)
		assert.Equal(t, "COMMITTED", st)
	}
}

func TestRepository_MarkFailed(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()
	eid := uuid.New()

	require.NoError(t, repo.InsertPending(ctx, eid, "picking"))
	require.NoError(t, repo.MarkFailed(ctx, []uuid.UUID{eid}, "0xbadtx", "Invalid status transition"))

	st, err := repo.Status(ctx, eid)
	require.NoError(t, err)
	assert.Equal(t, "FAILED", st)
}

func TestRepository_EmptyBatch_NoOp(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()

	// Все mark-методы на пустом []uuid.UUID должны просто вернуть nil
	// без SQL вызова (защита от пустого ANY($1)).
	assert.NoError(t, repo.MarkSent(ctx, nil, "0x"))
	assert.NoError(t, repo.MarkCommitted(ctx, nil))
	assert.NoError(t, repo.MarkFailed(ctx, nil, "", "reason"))
}
