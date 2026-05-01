package shipping

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbTX interface {
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

func (r *Repository) GetBinWithDestinationByID(ctx context.Context, binID uuid.UUID) (*bufferBinRecord, error) {
	const query = `
		SELECT b.bin_id, b.code, b.section, d.destination_id, d.code, d.name
		FROM wms_inventory.bins b
		LEFT JOIN wms_inventory.destinations d ON d.destination_id = b.destination_id
		WHERE b.bin_id = $1`

	var bin bufferBinRecord
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

func (r *Repository) ListReadyToShipProductsByBin(ctx context.Context, binID uuid.UUID) ([]readyToShipProduct, error) {
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

	products := make([]readyToShipProduct, 0)
	for rows.Next() {
		var p readyToShipProduct
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

func (r *Repository) GetDispatchByCode(ctx context.Context, dispatchCode string) (*dispatchRecord, error) {
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

func (r *Repository) UpdateDispatchToAtGate(ctx context.Context, dispatchCode string) (*dispatchRecord, error) {
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

func (r *Repository) scanDispatch(ctx context.Context, query string, args ...any) (*dispatchRecord, error) {
	var dispatch dispatchRecord
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
