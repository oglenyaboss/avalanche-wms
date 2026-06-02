package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repository is read-only: it only reads existing tables/views and never writes,
// so it holds just a query handle (no *pgxpool.Pool / WithTx, like destinations).
type Repository struct {
	q dbTX
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{q: db}
}

// GetSummary returns headline counters and lifecycle breakdowns. EventsToday is
// the number of outbox events created since local midnight.
func (r *Repository) GetSummary(ctx context.Context) (SummaryReport, error) {
	products, err := r.statusCounts(ctx, `SELECT status::text, count(*) FROM wms_inventory.products GROUP BY status`)
	if err != nil {
		return SummaryReport{}, fmt.Errorf("analytics.Repository.GetSummary products: %w", err)
	}
	orders, err := r.statusCounts(ctx, `SELECT status::text, count(*) FROM wms_inventory.orders GROUP BY status`)
	if err != nil {
		return SummaryReport{}, fmt.Errorf("analytics.Repository.GetSummary orders: %w", err)
	}
	dispatches, err := r.statusCounts(ctx, `SELECT status::text, count(*) FROM wms_inventory.outbound_dispatches GROUP BY status`)
	if err != nil {
		return SummaryReport{}, fmt.Errorf("analytics.Repository.GetSummary dispatches: %w", err)
	}

	var t Totals
	if err := r.q.QueryRow(ctx, `SELECT
        (SELECT count(*) FROM wms_inventory.skus),
        (SELECT count(*) FROM wms_inventory.products),
        (SELECT count(*) FROM wms_inventory.orders),
        (SELECT count(*) FROM wms_inventory.outbound_dispatches),
        (SELECT count(*) FROM wms_inventory.destinations)`).
		Scan(&t.Skus, &t.Products, &t.Orders, &t.Dispatches, &t.Destinations); err != nil {
		return SummaryReport{}, fmt.Errorf("analytics.Repository.GetSummary totals: %w", err)
	}

	var eventsToday int64
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM public.outbox_events WHERE created_at >= current_date`).
		Scan(&eventsToday); err != nil {
		return SummaryReport{}, fmt.Errorf("analytics.Repository.GetSummary eventsToday: %w", err)
	}

	return SummaryReport{
		Totals:             t,
		EventsToday:        eventsToday,
		ProductsByStatus:   toBuckets(products),
		OrdersByStatus:     toBuckets(orders),
		DispatchesByStatus: toBuckets(dispatches),
	}, nil
}

// GetOnchain returns the blockchain confirmation report. failedLimit and
// committedLimit bound the recent-event feeds.
func (r *Repository) GetOnchain(ctx context.Context, failedLimit, committedLimit int) (OnchainReport, error) {
	status, err := r.statusCounts(ctx, `SELECT status::text, count(*) FROM public.onchain_events GROUP BY status`)
	if err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Repository.GetOnchain status: %w", err)
	}

	byAgg, err := r.aggStatusCounts(ctx,
		`SELECT aggregate_type, status::text, count(*) FROM public.onchain_events GROUP BY aggregate_type, status`)
	if err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Repository.GetOnchain byAgg: %w", err)
	}

	var outboxTotal int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM public.outbox_events`).Scan(&outboxTotal); err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Repository.GetOnchain outboxTotal: %w", err)
	}

	outboxByType, err := r.statusCounts(ctx,
		`SELECT aggregate_type, count(*) FROM public.outbox_events GROUP BY aggregate_type`)
	if err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Repository.GetOnchain outboxByType: %w", err)
	}

	failed, err := r.eventRefs(ctx,
		`SELECT event_id::text, aggregate_type, tx_hash, error_message, updated_at
         FROM public.onchain_events WHERE status = 'FAILED'
         ORDER BY updated_at DESC LIMIT $1`, failedLimit)
	if err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Repository.GetOnchain failed: %w", err)
	}

	committed, err := r.eventRefs(ctx,
		`SELECT event_id::text, aggregate_type, tx_hash, error_message, updated_at
         FROM public.onchain_events WHERE status = 'COMMITTED'
         ORDER BY updated_at DESC LIMIT $1`, committedLimit)
	if err != nil {
		return OnchainReport{}, fmt.Errorf("analytics.Repository.GetOnchain committed: %w", err)
	}

	return deriveOnchain(status, byAgg, outboxTotal, outboxByType, failed, committed), nil
}

// GetThroughput returns daily event volume per stage over the trailing `days`
// window (inclusive of today), pivoted onto a gap-free axis.
func (r *Repository) GetThroughput(ctx context.Context, days int) (ThroughputReport, error) {
	rows, err := r.q.Query(ctx,
		`SELECT date_trunc('day', created_at)::date AS day, aggregate_type, count(*)
         FROM public.outbox_events
         WHERE created_at >= current_date - ($1::int - 1)
         GROUP BY 1, 2
         ORDER BY 1, 2`, days)
	if err != nil {
		return ThroughputReport{}, fmt.Errorf("analytics.Repository.GetThroughput query: %w", err)
	}
	defer rows.Close()

	var out []throughputRow
	for rows.Next() {
		var tr throughputRow
		if err := rows.Scan(&tr.Day, &tr.AggregateType, &tr.Count); err != nil {
			return ThroughputReport{}, fmt.Errorf("analytics.Repository.GetThroughput scan: %w", err)
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return ThroughputReport{}, fmt.Errorf("analytics.Repository.GetThroughput rows: %w", err)
	}
	return pivotThroughput(out, days, time.Now()), nil
}

// statusCounts runs a two-column (label, count) aggregate query. The first
// column is stored in statusCount.Status regardless of whether it is a real
// status or an aggregate_type — callers interpret it.
func (r *Repository) statusCounts(ctx context.Context, query string) ([]statusCount, error) {
	rows, err := r.q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []statusCount
	for rows.Next() {
		var sc statusCount
		if err := rows.Scan(&sc.Status, &sc.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (r *Repository) aggStatusCounts(ctx context.Context, query string) ([]aggStatusCount, error) {
	rows, err := r.q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []aggStatusCount
	for rows.Next() {
		var a aggStatusCount
		if err := rows.Scan(&a.AggregateType, &a.Status, &a.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) eventRefs(ctx context.Context, query string, limit int) ([]eventRef, error) {
	rows, err := r.q.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []eventRef
	for rows.Next() {
		var e eventRef
		if err := rows.Scan(&e.EventID, &e.AggregateType, &e.TxHash, &e.ErrorMessage, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func toBuckets(rows []statusCount) []StatusBucket {
	out := make([]StatusBucket, 0, len(rows))
	for _, r := range rows {
		// statusCount and StatusBucket are structurally identical (status, count).
		out = append(out, StatusBucket(r))
	}
	return out
}
