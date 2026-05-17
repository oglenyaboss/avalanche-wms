package shipping

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repository struct {
	db *pgxpool.Pool
	q  dbTX
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db, q: db}
}

func (r *Repository) WithTx(ctx context.Context, fn func(shippingRepository) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("shipping.Repository.WithTx begin: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(&Repository{db: r.db, q: tx}); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("shipping.Repository.WithTx commit: %w", err)
	}

	committed = true
	return nil
}

func (r *Repository) GetBinWithDestinationByID(ctx context.Context, binID uuid.UUID) (*BufferBinRecord, error) {
	const query = `
		SELECT b.bin_id, b.code, b.section, d.destination_id, d.code, d.name
		FROM wms_inventory.bins b
		LEFT JOIN wms_inventory.destinations d ON d.destination_id = b.destination_id
		WHERE b.bin_id = $1`

	var bin BufferBinRecord
	err := r.q.QueryRow(ctx, query, binID).Scan(
		&bin.BinID,
		&bin.Code,
		&bin.Section,
		&bin.DestinationID,
		&bin.DestinationCode,
		&bin.DestinationName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("shipping.Repository.GetBinWithDestinationByID: %w", ErrBinNotFound)
		}
		return nil, fmt.Errorf("shipping.Repository.GetBinWithDestinationByID scan: %w", err)
	}

	return &bin, nil
}

func (r *Repository) GetBufferBinForUpdate(ctx context.Context, binID uuid.UUID) (*BufferBinRecord, error) {
	const query = `
		SELECT b.bin_id, b.code, b.section, d.destination_id, d.code, d.name
		FROM wms_inventory.bins b
		LEFT JOIN wms_inventory.destinations d ON d.destination_id = b.destination_id
		WHERE b.bin_id = $1
		FOR UPDATE OF b`

	var bin BufferBinRecord
	err := r.q.QueryRow(ctx, query, binID).Scan(
		&bin.BinID,
		&bin.Code,
		&bin.Section,
		&bin.DestinationID,
		&bin.DestinationCode,
		&bin.DestinationName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("shipping.Repository.GetBufferBinForUpdate: %w", ErrBinNotFound)
		}
		return nil, fmt.Errorf("shipping.Repository.GetBufferBinForUpdate scan: %w", err)
	}

	return &bin, nil
}

func (r *Repository) ListReadyToShipProductsByBin(ctx context.Context, binID uuid.UUID) ([]ReadyToShipProduct, error) {
	const query = `
		SELECT p.product_id, p.qr_code, s.name, o.external_order_no
		FROM wms_inventory.products p
		JOIN wms_inventory.skus s ON s.sku_id = p.sku_id
		LEFT JOIN wms_inventory.orders o ON o.order_id = p.order_id
		WHERE p.bin_id = $1 AND p.status = 'READY_TO_SHIP'
		ORDER BY p.created_at`

	rows, err := r.q.Query(ctx, query, binID)
	if err != nil {
		return nil, fmt.Errorf("shipping.Repository.ListReadyToShipProductsByBin query: %w", err)
	}
	defer rows.Close()

	products := make([]ReadyToShipProduct, 0)
	for rows.Next() {
		var p ReadyToShipProduct
		if err := rows.Scan(&p.ProductID, &p.QRCode, &p.SKUName, &p.OrderExternalNo); err != nil {
			return nil, fmt.Errorf("shipping.Repository.ListReadyToShipProductsByBin scan: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("shipping.Repository.ListReadyToShipProductsByBin rows: %w", err)
	}

	return products, nil
}

func (r *Repository) GetDispatchByCode(ctx context.Context, dispatchCode string) (*DispatchRecord, error) {
	const query = `
		SELECT d.dispatch_id, d.dispatch_code, d.destination_id, dest.code, dest.name,
		       d.vehicle_number, d.driver_name, d.driver_phone, d.status, d.arrived_at
		FROM wms_inventory.outbound_dispatches d
		JOIN wms_inventory.destinations dest ON dest.destination_id = d.destination_id
		WHERE d.dispatch_code = $1`

	dispatch, err := r.scanDispatch(ctx, query, dispatchCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("shipping.Repository.GetDispatchByCode: %w", ErrDispatchNotFound)
		}
		return nil, fmt.Errorf("shipping.Repository.GetDispatchByCode scan: %w", err)
	}

	return dispatch, nil
}

func (r *Repository) GetDispatchForUpdate(ctx context.Context, dispatchID uuid.UUID) (*DispatchRecord, error) {
	const query = `
		SELECT d.dispatch_id, d.dispatch_code, d.destination_id, dest.code, dest.name,
		       d.vehicle_number, d.driver_name, d.driver_phone, d.status, d.arrived_at
		FROM wms_inventory.outbound_dispatches d
		JOIN wms_inventory.destinations dest ON dest.destination_id = d.destination_id
		WHERE d.dispatch_id = $1
		FOR UPDATE OF d`

	dispatch, err := r.scanDispatch(ctx, query, dispatchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("shipping.Repository.GetDispatchForUpdate: %w", ErrDispatchNotFound)
		}
		return nil, fmt.Errorf("shipping.Repository.GetDispatchForUpdate scan: %w", err)
	}

	return dispatch, nil
}

func (r *Repository) UpdateDispatchToAtGate(ctx context.Context, dispatchCode string) (*DispatchRecord, error) {
	const query = `
		UPDATE wms_inventory.outbound_dispatches d
		SET status = 'AT_GATE', arrived_at = NOW(), updated_at = NOW()
		FROM wms_inventory.destinations dest
		WHERE d.dispatch_code = $1
		  AND d.status = 'SCHEDULED'
		  AND dest.destination_id = d.destination_id
		RETURNING d.dispatch_id, d.dispatch_code, d.destination_id, dest.code, dest.name,
		          d.vehicle_number, d.driver_name, d.driver_phone, d.status, d.arrived_at`

	dispatch, err := r.scanDispatch(ctx, query, dispatchCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("shipping.Repository.UpdateDispatchToAtGate: %w", ErrDispatchNotFound)
		}
		return nil, fmt.Errorf("shipping.Repository.UpdateDispatchToAtGate scan: %w", err)
	}

	return dispatch, nil
}

func (r *Repository) scanDispatch(ctx context.Context, query string, args ...any) (*DispatchRecord, error) {
	var dispatch DispatchRecord
	err := r.q.QueryRow(ctx, query, args...).Scan(
		&dispatch.DispatchID,
		&dispatch.DispatchCode,
		&dispatch.DestinationID,
		&dispatch.DestinationCode,
		&dispatch.DestinationName,
		&dispatch.VehicleNumber,
		&dispatch.DriverName,
		&dispatch.DriverPhone,
		&dispatch.Status,
		&dispatch.ArrivedAt,
	)
	if err != nil {
		return nil, err
	}

	return &dispatch, nil
}

func (r *Repository) SelectProductsForShip(
	ctx context.Context,
	binID uuid.UUID,
	productIDs []uuid.UUID,
) ([]ProductForShip, error) {
	query := `
		SELECT product_id, order_id
		FROM wms_inventory.products
		WHERE bin_id = $1 AND status = 'READY_TO_SHIP'`
	args := []any{binID}
	if len(productIDs) > 0 {
		query += ` AND product_id = ANY($2::uuid[])`
		args = append(args, productIDs)
	}
	query += ` ORDER BY created_at FOR UPDATE`

	rows, err := r.q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("shipping.Repository.SelectProductsForShip query: %w", err)
	}
	defer rows.Close()

	products := make([]ProductForShip, 0)
	for rows.Next() {
		var p ProductForShip
		if err := rows.Scan(&p.ProductID, &p.OrderID); err != nil {
			return nil, fmt.Errorf("shipping.Repository.SelectProductsForShip scan: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("shipping.Repository.SelectProductsForShip rows: %w", err)
	}

	return products, nil
}

func (r *Repository) BatchUpdateProductsShipped(ctx context.Context, productIDs []uuid.UUID) error {
	if len(productIDs) == 0 {
		return nil
	}

	const query = `
		UPDATE wms_inventory.products
		SET status = 'SHIPPED'
		WHERE product_id = ANY($1::uuid[])`

	tag, err := r.q.Exec(ctx, query, productIDs)
	if err != nil {
		return fmt.Errorf("shipping.Repository.BatchUpdateProductsShipped exec: %w", err)
	}
	if int(tag.RowsAffected()) != len(productIDs) {
		return fmt.Errorf("shipping.Repository.BatchUpdateProductsShipped affected %d want %d: %w",
			tag.RowsAffected(), len(productIDs), ErrProductNotInBuffer)
	}

	return nil
}

func (r *Repository) BatchInsertShippings(
	ctx context.Context,
	events []shippingEvent,
	dispatchID uuid.UUID,
	operatorID uuid.UUID,
) error {
	if len(events) == 0 {
		return nil
	}

	eventIDs := make([]uuid.UUID, 0, len(events))
	productIDs := make([]uuid.UUID, 0, len(events))
	for _, event := range events {
		eventIDs = append(eventIDs, event.EventID)
		productIDs = append(productIDs, event.ProductID)
	}

	const query = `
		INSERT INTO wms_ops.shippings (
			event_id,
			product_id,
			dispatch_id,
			operator_id,
			onchain_status,
			shipped_at,
			occurred_at
		)
		SELECT
			event_id,
			product_id,
			$3,
			$4,
			'PENDING_ONCHAIN',
			NOW(),
			NOW()
		FROM unnest($1::uuid[], $2::uuid[]) AS events(event_id, product_id)`

	if _, err := r.q.Exec(ctx, query, eventIDs, productIDs, dispatchID, operatorID); err != nil {
		return fmt.Errorf("shipping.Repository.BatchInsertShippings exec: %w", err)
	}

	return nil
}

func (r *Repository) BatchInsertShippingOutbox(
	ctx context.Context,
	events []shippingEvent,
	dispatchID uuid.UUID,
) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	eventIDs := make([]uuid.UUID, 0, len(events))
	aggregateIDs := make([]uuid.UUID, 0, len(events))
	payloadHashes := make([]string, 0, len(events))
	for _, event := range events {
		payloadHash, err := payloadHashForShipping(event.ProductID, dispatchID)
		if err != nil {
			return 0, fmt.Errorf("shipping.Repository.BatchInsertShippingOutbox build payload hash: %w", err)
		}
		eventIDs = append(eventIDs, event.EventID)
		aggregateIDs = append(aggregateIDs, event.ProductID)
		payloadHashes = append(payloadHashes, payloadHash)
	}

	const query = `
		INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
		SELECT event_id, aggregate_id, 'shipping', 'wms.shipping.v1', payload_hash
		FROM unnest($1::uuid[], $2::uuid[], $3::text[]) AS events(event_id, aggregate_id, payload_hash)`

	tag, err := r.q.Exec(ctx, query, eventIDs, aggregateIDs, payloadHashes)
	if err != nil {
		return 0, fmt.Errorf("shipping.Repository.BatchInsertShippingOutbox exec: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

func (r *Repository) UpdateOrdersShippedConditional(ctx context.Context, orderIDs []uuid.UUID) (int, error) {
	if len(orderIDs) == 0 {
		return 0, nil
	}

	const query = `
		UPDATE wms_inventory.orders
		SET status = 'SHIPPED', updated_at = NOW()
		WHERE order_id = ANY($1::uuid[])
		  AND NOT EXISTS (
			SELECT 1
			FROM wms_inventory.products
			WHERE order_id = orders.order_id AND status != 'SHIPPED'
		  )`

	tag, err := r.q.Exec(ctx, query, orderIDs)
	if err != nil {
		return 0, fmt.Errorf("shipping.Repository.UpdateOrdersShippedConditional exec: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

func (r *Repository) CountReadyToShipProductsInBuffer(ctx context.Context, binID uuid.UUID) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wms_inventory.products
		WHERE bin_id = $1 AND status = 'READY_TO_SHIP'`

	var count int
	if err := r.q.QueryRow(ctx, query, binID).Scan(&count); err != nil {
		return 0, fmt.Errorf("shipping.Repository.CountReadyToShipProductsInBuffer scan: %w", err)
	}

	return count, nil
}

func (r *Repository) UpdateDispatchDeparted(ctx context.Context, dispatchID uuid.UUID) error {
	const query = `
		UPDATE wms_inventory.outbound_dispatches
		SET status = 'DEPARTED', departed_at = NOW(), updated_at = NOW()
		WHERE dispatch_id = $1 AND status = 'AT_GATE'`

	tag, err := r.q.Exec(ctx, query, dispatchID)
	if err != nil {
		return fmt.Errorf("shipping.Repository.UpdateDispatchDeparted exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("shipping.Repository.UpdateDispatchDeparted: %w", ErrDispatchNotAtGate)
	}

	return nil
}
