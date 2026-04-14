package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository — persistence слой для onchain_events.
//
// Таблица: public.onchain_events (event_id UUID unique, aggregate_type text,
// tx_hash text, status onchain_event_status ENUM('PENDING','SENT','COMMITTED',
// 'FAILED'), error_message text, created_at/updated_at).
//
// Lifecycle: InsertPending → MarkSent(txHash) → MarkCommitted | MarkFailed.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository оборачивает pool.
func NewRepository(p *pgxpool.Pool) *Repository { return &Repository{pool: p} }

// Exists возвращает true если event_id уже виден в onchain_events (любой статус).
// Нужно для idempotency-check в consumer'е при рестартах.
func (r *Repository) Exists(ctx context.Context, eventID uuid.UUID) (bool, error) {
	var one int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM public.onchain_events WHERE event_id=$1`, eventID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("exists %s: %w", eventID, err)
	}
	return true, nil
}

// InsertPending идемпотентно создаёт запись в PENDING. ON CONFLICT — no-op.
func (r *Repository) InsertPending(ctx context.Context, eventID uuid.UUID, aggType string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO public.onchain_events (event_id, aggregate_type, status)
		 VALUES ($1, $2, 'PENDING')
		 ON CONFLICT (event_id) DO NOTHING`,
		eventID, aggType)
	if err != nil {
		return fmt.Errorf("insert pending %s: %w", eventID, err)
	}
	return nil
}

// MarkSent выставляет status=SENT и tx_hash для всех events в батче (UUID[]).
func (r *Repository) MarkSent(ctx context.Context, ids []uuid.UUID, txHash string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE public.onchain_events
		 SET tx_hash=$2, status='SENT', updated_at=now()
		 WHERE event_id = ANY($1)`,
		ids, txHash)
	if err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	return nil
}

// MarkCommitted — финальный успех, receipt.Status=1.
func (r *Repository) MarkCommitted(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE public.onchain_events
		 SET status='COMMITTED', updated_at=now()
		 WHERE event_id = ANY($1)`,
		ids)
	if err != nil {
		return fmt.Errorf("mark committed: %w", err)
	}
	return nil
}

// MarkFailed — receipt.Status=0 или EstimateGas revert. txHash опционален
// (может быть пусто если tx даже не был отправлен). reason — человекочитаемое
// описание ошибки.
func (r *Repository) MarkFailed(ctx context.Context, ids []uuid.UUID, txHash, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE public.onchain_events
		 SET status='FAILED',
		     tx_hash = COALESCE(NULLIF($2,''), tx_hash),
		     error_message=$3,
		     updated_at=now()
		 WHERE event_id = ANY($1)`,
		ids, txHash, reason)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

// Status возвращает текущий статус по event_id (для тестов / debugging).
func (r *Repository) Status(ctx context.Context, eventID uuid.UUID) (string, error) {
	var s string
	err := r.pool.QueryRow(ctx,
		`SELECT status::text FROM public.onchain_events WHERE event_id=$1`,
		eventID).Scan(&s)
	if err != nil {
		return "", fmt.Errorf("status %s: %w", eventID, err)
	}
	return s, nil
}
