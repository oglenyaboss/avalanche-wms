package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Aggregate types written to public.outbox_events by each WMS stage. They are the
// aggregateType argument to CheckChainStatus and MUST match the literals the
// repositories insert (all lowercase; the assembly/pick step is "picking", not
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

// actionTypeByAggregate maps the aggregate type of each stage to the signed
// action type stored in public.warehouse_actions (ADR-0001 §3).
var actionTypeByAggregate = map[string]int16{
	AggregateReceiving: 1,
	AggregatePutaway:   2,
	AggregatePicking:   3,
	AggregateShipping:  4,
}

// CheckChainStatus is the WMS->chain status gate (issue #45). It returns
// ErrChainEventRejected if any of productIDs has a FAILED on-chain event for
// aggregateType, or is contained in a FAILED signed action of that stage's type
// (legacy per-product events and multi-product signed actions), letting the
// caller reject the next FSM step before mutating state.
//
// public.onchain_events has no product_id, so the lookup joins through
// public.outbox_events (aggregate_id = product_id); signed actions are matched
// through warehouse_actions.item_ids. A missing event or any non-FAILED
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
			FROM public.onchain_events oe
			JOIN public.outbox_events ob USING (event_id)
			WHERE ob.aggregate_id = ANY($1::uuid[])
			  AND ob.aggregate_type = $2
			  AND oe.status = 'FAILED'
		) OR EXISTS (
			SELECT 1
			FROM public.warehouse_actions wa
			JOIN public.onchain_events oe ON oe.event_id = wa.action_id
			WHERE wa.item_ids && $1::uuid[]
			  AND wa.action_type = $3
			  AND oe.status = 'FAILED'
		)`

	var rejected bool
	if err := q.QueryRow(ctx, query, productIDs, aggregateType, actionTypeByAggregate[aggregateType]).Scan(&rejected); err != nil {
		return fmt.Errorf("ledger.CheckChainStatus: %w", err)
	}
	if rejected {
		return ErrChainEventRejected
	}
	return nil
}
