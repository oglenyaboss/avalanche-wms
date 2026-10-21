package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Aggregate* name the warehouse stages. Callers pass them to CheckChainStatus and
// UnconfirmedProducts, which map the stage to the signed action type via
// actionTypeByAggregate (all lowercase; the assembly/pick step is "picking", not
// "assembly"). Kept here as the single source of truth shared by the gate and tests.
const (
	AggregateReceiving = "receiving"
	AggregatePutaway   = "putaway"
	AggregatePicking   = "picking"
	AggregateShipping  = "shipping"
)

// ErrChainEventRejected indicates a product's on-chain event for a given stage is
// FAILED, so the WMS must not advance that product to the next stage. Handlers map
// this to HTTP 409.
var ErrChainEventRejected = errors.New("CHAIN_EVENT_REJECTED")

// RowQuerier is the subset of pgx needed by CheckChainStatus; satisfied by both a
// *pgxpool.Pool and a pgx.Tx.
type RowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RowsQuerier is the subset of pgx needed by UnconfirmedProducts (multi-row
// result); satisfied by both a *pgxpool.Pool and a pgx.Tx.
type RowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// actionTypeByAggregate maps the aggregate type of each stage to the signed
// action type stored in public.warehouse_actions (ADR-0001 §3).
var actionTypeByAggregate = map[string]int16{
	AggregateReceiving: 1,
	AggregatePutaway:   2,
	AggregatePicking:   3,
	AggregateShipping:  4,
}

// CheckChainStatus is the WMS->chain status gate (issue #45). It returns
// ErrChainEventRejected if any of productIDs is contained in a FAILED signed
// action of that stage's type, letting the caller reject the next FSM step
// before mutating state.
//
// public.onchain_events has no product_id, so the lookup matches signed actions
// through warehouse_actions.item_ids. A missing action or any non-FAILED
// status passes (nil): a product whose previous stage has not yet been mirrored
// on-chain, or is still PENDING/SENT/COMMITTED, is on the happy path. Only a confirmed
// FAILED blocks — transient stuck PENDING/SENT rows are pulled forward by the
// ledger-adapter reconciler, not gated here, so the gate never produces timing-dependent
// 409s on the happy path.
func CheckChainStatus(ctx context.Context, q RowQuerier, productIDs []uuid.UUID, aggregateType string) error {
	// No products to gate (e.g. an empty batch): nothing to check, pass. Callers gate
	// against empty input before reaching here (putaway via ErrInvalidInput, Pick passes
	// exactly one id), so this is defensive, not a fail-open path.
	if len(productIDs) == 0 {
		return nil
	}

	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM public.warehouse_actions wa
			JOIN public.onchain_events oe ON oe.event_id = wa.action_id
			WHERE wa.item_ids && $1::uuid[]
			  AND wa.action_type = $2
			  AND oe.status = 'FAILED'
		)`

	var rejected bool
	if err := q.QueryRow(ctx, query, productIDs, actionTypeByAggregate[aggregateType]).Scan(&rejected); err != nil {
		return fmt.Errorf("ledger.CheckChainStatus: %w", err)
	}
	if rejected {
		return ErrChainEventRejected
	}
	return nil
}

// UnconfirmedProducts returns the subset of productIDs whose stage (the
// aggregate's stage, e.g. picking) has NO COMMITTED mirror on chain as a signed
// action of that stage (issue #15 gates Ship on confirmed picking). The result is
// sorted; confirmed or not-yet-written products are not returned.
func UnconfirmedProducts(ctx context.Context, q RowsQuerier, productIDs []uuid.UUID, aggregate string) ([]uuid.UUID, error) {
	// No products to check: nothing is unconfirmed.
	if len(productIDs) == 0 {
		return nil, nil
	}

	const query = `
		SELECT p.product_id
		FROM unnest($1::uuid[]) AS p(product_id)
		LEFT JOIN LATERAL (
			SELECT true AS confirmed
			FROM public.warehouse_actions wa
			WHERE wa.item_ids @> ARRAY[p.product_id]
			  AND wa.action_type = $2
			  AND wa.submitted_at IS NOT NULL
			  AND (SELECT oe.status FROM public.onchain_events oe WHERE oe.event_id = wa.action_id) = 'COMMITTED'
			LIMIT 1
		) c ON true
		WHERE c.confirmed IS NULL`

	rows, err := q.Query(ctx, query, pgx.QueryExecModeDescribeExec, productIDs, actionTypeByAggregate[aggregate])
	if err != nil {
		return nil, fmt.Errorf("ledger.UnconfirmedProducts: %w", err)
	}
	defer rows.Close()

	unconfirmed := make([]uuid.UUID, 0, len(productIDs))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("ledger.UnconfirmedProducts scan: %w", err)
		}
		unconfirmed = append(unconfirmed, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger.UnconfirmedProducts rows: %w", err)
	}

	// Сортировка в Go: результат детерминирован независимо от плана запроса.
	slices.SortFunc(unconfirmed, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return unconfirmed, nil
}
