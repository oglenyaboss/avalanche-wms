package products

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProductNotFound — the key resolved to no product (404 at the handler).
var ErrProductNotFound = errors.New("product not found")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// Recent returns the most recently touched products (for the lookup list).
func (r *Repository) Recent(ctx context.Context, limit int) ([]RecentProduct, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.product_id::text, p.qr_code, s.name, p.status::text, p.updated_at
		FROM wms_inventory.products p
		JOIN wms_inventory.skus s ON s.sku_id = p.sku_id
		ORDER BY p.updated_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("products.Recent: %w", err)
	}
	defer rows.Close()

	out := make([]RecentProduct, 0)
	for rows.Next() {
		var (
			p       RecentProduct
			updated time.Time
		)
		if err := rows.Scan(&p.ProductID, &p.QRCode, &p.SKUName, &p.Status, &updated); err != nil {
			return nil, fmt.Errorf("products.Recent scan: %w", err)
		}
		p.UpdatedAt = updated.UTC().Format(time.RFC3339)
		out = append(out, p)
	}
	return out, rows.Err()
}

// resolveProduct accepts a product_id (UUID) or a qr_code and returns the header.
func (r *Repository) resolveProduct(ctx context.Context, key string) (ProductHeader, error) {
	const base = `SELECT p.product_id::text, p.qr_code, s.name, p.status::text
	              FROM wms_inventory.products p
	              JOIN wms_inventory.skus s ON s.sku_id = p.sku_id
	              WHERE %s = $1`
	var (
		query string
		arg   any
	)
	if id, err := uuid.Parse(key); err == nil {
		query = fmt.Sprintf(base, "p.product_id")
		arg = id
	} else {
		query = fmt.Sprintf(base, "p.qr_code")
		arg = key
	}
	var h ProductHeader
	if err := r.db.QueryRow(ctx, query, arg).Scan(&h.ProductID, &h.QRCode, &h.SKUName, &h.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductHeader{}, ErrProductNotFound
		}
		return ProductHeader{}, fmt.Errorf("products.resolveProduct: %w", err)
	}
	return h, nil
}

// Timeline returns the product header plus all of its outbox events joined to
// their on-chain status, oldest first. Multiple rows per stage (re-putaway etc.)
// are returned as-is.
func (r *Repository) Timeline(ctx context.Context, key string) (Timeline, error) {
	header, err := r.resolveProduct(ctx, key)
	if err != nil {
		return Timeline{}, err
	}
	rows, err := r.db.Query(ctx, `
		SELECT oe.aggregate_type, oe.event_type, oe.created_at, oe.event_id::text,
		       onc.tx_hash, COALESCE(onc.status::text, 'PENDING'),
		       onc.updated_at, onc.error_message
		FROM public.outbox_events oe
		LEFT JOIN public.onchain_events onc USING (event_id)
		WHERE oe.aggregate_id = $1::uuid
		ORDER BY oe.created_at ASC`, header.ProductID)
	if err != nil {
		return Timeline{}, fmt.Errorf("products.Timeline steps: %w", err)
	}
	defer rows.Close()

	steps := make([]TimelineStep, 0)
	for rows.Next() {
		var (
			st       TimelineStep
			occurred time.Time
			chainUpd *time.Time
		)
		if err := rows.Scan(&st.Stage, &st.EventType, &occurred, &st.EventID,
			&st.TxHash, &st.ChainStatus, &chainUpd, &st.ErrorMessage); err != nil {
			return Timeline{}, fmt.Errorf("products.Timeline scan: %w", err)
		}
		st.OccurredAt = occurred.UTC().Format(time.RFC3339)
		if chainUpd != nil {
			s := chainUpd.UTC().Format(time.RFC3339)
			st.ChainUpdatedAt = &s
		}
		steps = append(steps, st)
	}
	if err := rows.Err(); err != nil {
		return Timeline{}, fmt.Errorf("products.Timeline rows: %w", err)
	}
	return Timeline{Product: header, Steps: steps}, nil
}
