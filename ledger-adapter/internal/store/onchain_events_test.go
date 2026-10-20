//go:build integration

// Integration-тесты требуют Docker (testcontainers). Запускать локально:
//   go test -tags integration ./internal/store/... -race -timeout 120s
// В CI требует DinD service — добавить отдельным job'ом.

package store

import (
	"context"
	"database/sql"
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

	// Применить схему onchain_events (минимальный срез wms/migrations/0001
	// + reject_reason из 0015_key_actions).
	_, err = pool.Exec(ctx, `
		CREATE TYPE onchain_event_status AS ENUM ('PENDING','SENT','COMMITTED','FAILED');
		CREATE TABLE public.onchain_events (
			id bigserial PRIMARY KEY,
			event_id uuid NOT NULL UNIQUE,
			aggregate_type text NOT NULL,
			tx_hash text,
			status onchain_event_status NOT NULL DEFAULT 'PENDING',
			reject_reason smallint,
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

func TestRepository_MarkRejected(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()
	eid, committed := uuid.New(), uuid.New()

	require.NoError(t, repo.InsertPending(ctx, eid, "signed_action"))
	require.NoError(t, repo.InsertPending(ctx, committed, "signed_action"))
	// Guard-кейс: строка, которую reconcile уже довёл до COMMITTED.
	require.NoError(t, repo.MarkCommitted(ctx, []uuid.UUID{committed}))

	require.NoError(t, repo.MarkRejected(ctx, []uuid.UUID{eid}, "0xbadtx", 6))
	require.NoError(t, repo.MarkRejected(ctx, []uuid.UUID{committed}, "0xbadtx", 6))

	var status string
	var reason sql.NullInt16
	var msg sql.NullString
	err := repo.pool.QueryRow(ctx,
		`SELECT status::text, reject_reason, error_message FROM public.onchain_events WHERE event_id=$1`,
		eid).Scan(&status, &reason, &msg)
	require.NoError(t, err)
	assert.Equal(t, "FAILED", status)
	require.True(t, reason.Valid, "reject_reason must be set")
	assert.Equal(t, int16(6), reason.Int16)
	assert.Equal(t, "action rejected: reason 6", msg.String)

	// Guard `status <> 'COMMITTED'`: COMMITTED не откатывается, reject_reason не появляется.
	err = repo.pool.QueryRow(ctx,
		`SELECT status::text, reject_reason, error_message FROM public.onchain_events WHERE event_id=$1`,
		committed).Scan(&status, &reason, &msg)
	require.NoError(t, err)
	assert.Equal(t, "COMMITTED", status)
	assert.False(t, reason.Valid, "reject_reason must stay NULL for committed row")
	assert.False(t, msg.Valid, "error_message must stay NULL for committed row")
}

func TestRepository_ListReconcilable(t *testing.T) {
	repo := setupTestDB(t)
	ctx := context.Background()
	e1, e2, eRejected, fresh := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	for _, e := range []struct {
		id  uuid.UUID
		agg string
	}{{e1, "receiving"}, {e2, "signed_action"}, {eRejected, "signed_action"}, {fresh, "picking"}} {
		require.NoError(t, repo.InsertPending(ctx, e.id, e.agg))
	}
	require.NoError(t, repo.MarkSent(ctx, []uuid.UUID{e1, e2, eRejected, fresh}, "0xfeed"))
	// Контрактное отклонение терминально — строки с reject_reason не реconcile'ятся.
	require.NoError(t, repo.MarkRejected(ctx, []uuid.UUID{eRejected}, "0xfeed", 6))
	// fresh остаётся свежим — minAge его отсекает; остальные бэкдейчим.
	_, err := repo.pool.Exec(ctx,
		`UPDATE public.onchain_events SET updated_at = now() - interval '1 hour' WHERE event_id = ANY($1)`,
		[]uuid.UUID{e1, e2, eRejected})
	require.NoError(t, err)

	rows, err := repo.ListReconcilable(ctx, time.Minute)
	require.NoError(t, err)
	got := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		got[row.EventID] = struct{}{}
	}
	assert.Contains(t, got, e2)
	assert.NotContains(t, got, e1, "legacy event-per-product row is not resolvable — must not starve the sweep window")
	assert.NotContains(t, got, eRejected, "rejected row is terminal — must not re-enter the sweep")
	assert.NotContains(t, got, fresh)
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
