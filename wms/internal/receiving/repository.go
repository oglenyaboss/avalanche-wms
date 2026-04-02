package receiving

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"wms/internal/domain"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

type GateLogParams struct {
	TTNCode        *string
	CargoplaceCode *string
	ShipmentID     *uuid.UUID
	CargoplaceID   *uuid.UUID
	OperatorID     uuid.UUID
	Action         string
	OccurredAt     time.Time
}

type TableLogParams struct {
	CargoplaceID uuid.UUID
	BoxID        *uuid.UUID
	OperatorID   uuid.UUID
	Action       string
	BoxBarcode   *string
	SKUID        *uuid.UUID
	QRCode       *string
	ProductID    *uuid.UUID
	BufferBinID  *uuid.UUID
	OccurredAt   time.Time
}

// GetShipmentByTTN retrieves an inbound shipment from the database based on the provided TTN code.
func (r *Repository) GetShipmentByTTN(ctx context.Context, ttnCode string) (*domain.InboundShipment, error) {
	const query = `
		SELECT shipment_id, warehouse_id, ttn_code, status, created_at, updated_at
		FROM wms_inventory.inbound_shipments
		WHERE ttn_code = $1`

	var shipment domain.InboundShipment
	err := r.db.QueryRow(ctx, query, ttnCode).Scan(
		&shipment.ShipmentID,
		&shipment.WarehouseID,
		&shipment.TTNCode,
		&shipment.Status,
		&shipment.CreatedAt,
		&shipment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetShipmentByTTN: %w", ErrTTNNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetShipmentByTTN scan: %w", err)
	}

	return &shipment, nil
}

func (r *Repository) GetShipmentByID(ctx context.Context, shipmentID uuid.UUID) (*domain.InboundShipment, error) {
	const query = `
		SELECT shipment_id, warehouse_id, ttn_code, status, created_at, updated_at
		FROM wms_inventory.inbound_shipments
		WHERE shipment_id = $1`

	var shipment domain.InboundShipment
	err := r.db.QueryRow(ctx, query, shipmentID).Scan(
		&shipment.ShipmentID,
		&shipment.WarehouseID,
		&shipment.TTNCode,
		&shipment.Status,
		&shipment.CreatedAt,
		&shipment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetShipmentByID: %w", ErrTTNNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetShipmentByID scan: %w", err)
	}

	return &shipment, nil
}

func (r *Repository) ListCargoplacesByShipment(
	ctx context.Context,
	shipmentID uuid.UUID,
) ([]domain.Cargoplace, error) {
	const query = `
		SELECT cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at
		FROM wms_inventory.cargoplaces
		WHERE shipment_id = $1
		ORDER BY cargoplace_code`

	rows, err := r.db.Query(ctx, query, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.ListCargoplacesByShipment query: %w", err)
	}
	defer rows.Close()

	result := make([]domain.Cargoplace, 0)
	for rows.Next() {
		var cp domain.Cargoplace
		if err := rows.Scan(
			&cp.CargoplaceID,
			&cp.ShipmentID,
			&cp.CargoplaceCode,
			&cp.Status,
			&cp.ReceivedAtGateAt,
			&cp.CreatedAt,
			&cp.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("receiving.Repository.ListCargoplacesByShipment scan: %w", err)
		}
		result = append(result, cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("receiving.Repository.ListCargoplacesByShipment rows: %w", err)
	}

	return result, nil
}

func (r *Repository) GetCargoplaceByShipmentAndCode(
	ctx context.Context,
	shipmentID uuid.UUID,
	cargoplaceCode string,
) (*domain.Cargoplace, error) {
	const query = `
		SELECT cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at
		FROM wms_inventory.cargoplaces
		WHERE shipment_id = $1 AND cargoplace_code = $2`

	var cp domain.Cargoplace
	err := r.db.QueryRow(ctx, query, shipmentID, cargoplaceCode).Scan(
		&cp.CargoplaceID,
		&cp.ShipmentID,
		&cp.CargoplaceCode,
		&cp.Status,
		&cp.ReceivedAtGateAt,
		&cp.CreatedAt,
		&cp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByShipmentAndCode: %w", ErrCargoplaceNotInShipment)
		}
		return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByShipmentAndCode scan: %w", err)
	}

	return &cp, nil
}

func (r *Repository) GetCargoplaceByID(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error) {
	const query = `
		SELECT cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at
		FROM wms_inventory.cargoplaces
		WHERE cargoplace_id = $1`

	var cp domain.Cargoplace
	err := r.db.QueryRow(ctx, query, cargoplaceID).Scan(
		&cp.CargoplaceID,
		&cp.ShipmentID,
		&cp.CargoplaceCode,
		&cp.Status,
		&cp.ReceivedAtGateAt,
		&cp.CreatedAt,
		&cp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByID: %w", ErrCargoplaceNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByID scan: %w", err)
	}

	return &cp, nil
}

func (r *Repository) UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, status string) error {
	const query = `
		UPDATE wms_inventory.inbound_shipments
		SET status = $2
		WHERE shipment_id = $1`

	tag, err := r.db.Exec(ctx, query, shipmentID, status)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateShipmentStatus exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateShipmentStatus: %w", ErrTTNNotFound)
	}

	return nil
}

func (r *Repository) UpdateCargoplaceReceivedAtGate(
	ctx context.Context,
	cargoplaceID uuid.UUID,
	status string,
	receivedAt time.Time,
) error {
	const query = `
		UPDATE wms_inventory.cargoplaces
		SET status = $2, received_at_gate_at = $3
		WHERE cargoplace_id = $1`

	tag, err := r.db.Exec(ctx, query, cargoplaceID, status, receivedAt)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceReceivedAtGate exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceReceivedAtGate: %w", ErrCargoplaceNotInShipment)
	}

	return nil
}

func (r *Repository) UpdateCargoplaceStatus(ctx context.Context, cargoplaceID uuid.UUID, status string) error {
	const query = `
		UPDATE wms_inventory.cargoplaces
		SET status = $2
		WHERE cargoplace_id = $1`

	tag, err := r.db.Exec(ctx, query, cargoplaceID, status)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceStatus exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceStatus: %w", ErrCargoplaceNotFound)
	}

	return nil
}

func (r *Repository) MarkExpectedAsNotReceived(
	ctx context.Context,
	shipmentID uuid.UUID,
	notReceivedStatus string,
) error {
	const query = `
		UPDATE wms_inventory.cargoplaces
		SET status = $2
		WHERE shipment_id = $1 AND status = 'EXPECTED'`

	if _, err := r.db.Exec(ctx, query, shipmentID, notReceivedStatus); err != nil {
		return fmt.Errorf("receiving.Repository.MarkExpectedAsNotReceived exec: %w", err)
	}
	return nil
}

func (r *Repository) CountCargoplaces(ctx context.Context, shipmentID uuid.UUID) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wms_inventory.cargoplaces
		WHERE shipment_id = $1`

	var total int
	if err := r.db.QueryRow(ctx, query, shipmentID).Scan(&total); err != nil {
		return 0, fmt.Errorf("receiving.Repository.CountCargoplaces scan: %w", err)
	}
	return total, nil
}

func (r *Repository) CountCargoplacesByStatus(ctx context.Context, shipmentID uuid.UUID, status string) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wms_inventory.cargoplaces
		WHERE shipment_id = $1 AND status = $2`

	var total int
	if err := r.db.QueryRow(ctx, query, shipmentID, status).Scan(&total); err != nil {
		return 0, fmt.Errorf("receiving.Repository.CountCargoplacesByStatus scan: %w", err)
	}
	return total, nil
}

func (r *Repository) ListExpectedSKUsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) ([]ExpectedSKU, error) {
	const query = `
		SELECT ecs.sku_id, s.name, ecs.expected_qty
		FROM wms_inventory.expected_cargoplace_skus ecs
		JOIN wms_inventory.skus s ON s.sku_id = ecs.sku_id
		WHERE ecs.cargoplace_id = $1
		ORDER BY s.name`

	rows, err := r.db.Query(ctx, query, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.ListExpectedSKUsByCargoplace query: %w", err)
	}
	defer rows.Close()

	items := make([]ExpectedSKU, 0)
	for rows.Next() {
		var item ExpectedSKU
		if err := rows.Scan(&item.SKUID, &item.SKUName, &item.ExpectedQty); err != nil {
			return nil, fmt.Errorf("receiving.Repository.ListExpectedSKUsByCargoplace scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("receiving.Repository.ListExpectedSKUsByCargoplace rows: %w", err)
	}

	return items, nil
}

func (r *Repository) UpsertBox(
	ctx context.Context,
	cargoplaceID uuid.UUID,
	boxBarcode string,
	status string,
) (*domain.Box, error) {
	const query = `
		INSERT INTO wms_inventory.boxes (box_id, cargoplace_id, box_barcode, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cargoplace_id, box_barcode)
		DO UPDATE SET status = EXCLUDED.status
		RETURNING box_id, cargoplace_id, box_barcode, status, created_at, updated_at`

	var box domain.Box
	err := r.db.QueryRow(ctx, query, uuid.New(), cargoplaceID, boxBarcode, status).Scan(
		&box.BoxID,
		&box.CargoplaceID,
		&box.BoxBarcode,
		&box.Status,
		&box.CreatedAt,
		&box.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.UpsertBox scan: %w", err)
	}

	return &box, nil
}

func (r *Repository) GetBoxByID(ctx context.Context, boxID uuid.UUID) (*domain.Box, error) {
	const query = `
		SELECT box_id, cargoplace_id, box_barcode, status, created_at, updated_at
		FROM wms_inventory.boxes
		WHERE box_id = $1`

	var box domain.Box
	err := r.db.QueryRow(ctx, query, boxID).Scan(
		&box.BoxID,
		&box.CargoplaceID,
		&box.BoxBarcode,
		&box.Status,
		&box.CreatedAt,
		&box.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetBoxByID: %w", ErrBoxNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetBoxByID scan: %w", err)
	}

	return &box, nil
}

func (r *Repository) GetBoxByCargoplaceAndBarcode(
	ctx context.Context,
	cargoplaceID uuid.UUID,
	boxBarcode string,
) (*domain.Box, error) {
	const query = `
		SELECT box_id, cargoplace_id, box_barcode, status, created_at, updated_at
		FROM wms_inventory.boxes
		WHERE cargoplace_id = $1 AND box_barcode = $2`

	var box domain.Box
	err := r.db.QueryRow(ctx, query, cargoplaceID, boxBarcode).Scan(
		&box.BoxID,
		&box.CargoplaceID,
		&box.BoxBarcode,
		&box.Status,
		&box.CreatedAt,
		&box.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetBoxByCargoplaceAndBarcode: %w", ErrBoxNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetBoxByCargoplaceAndBarcode scan: %w", err)
	}

	return &box, nil
}

func (r *Repository) UpdateBoxStatus(ctx context.Context, boxID uuid.UUID, status string) error {
	const query = `
		UPDATE wms_inventory.boxes
		SET status = $2
		WHERE box_id = $1`

	tag, err := r.db.Exec(ctx, query, boxID, status)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateBoxStatus exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateBoxStatus: %w", ErrBoxNotFound)
	}

	return nil
}

func (r *Repository) CountProductsByBox(ctx context.Context, boxID uuid.UUID) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wms_inventory.products
		WHERE box_id = $1`

	var total int
	if err := r.db.QueryRow(ctx, query, boxID).Scan(&total); err != nil {
		return 0, fmt.Errorf("receiving.Repository.CountProductsByBox scan: %w", err)
	}
	return total, nil
}

func (r *Repository) GetSKUByBarcode(ctx context.Context, barcode string) (*domain.SKU, error) {
	const query = `
		SELECT s.sku_id, s.name, s.description, s.volume, s.created_at, s.updated_at
		FROM wms_inventory.sku_barcodes sb
		JOIN wms_inventory.skus s ON s.sku_id = sb.sku_id
		WHERE sb.barcode = $1`

	var sku domain.SKU
	err := r.db.QueryRow(ctx, query, barcode).Scan(
		&sku.SKUID,
		&sku.Name,
		&sku.Description,
		&sku.Volume,
		&sku.CreatedAt,
		&sku.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetSKUByBarcode: %w", ErrBarcodeNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetSKUByBarcode scan: %w", err)
	}

	return &sku, nil
}

func (r *Repository) GetSKUByID(ctx context.Context, skuID uuid.UUID) (*domain.SKU, error) {
	const query = `
		SELECT sku_id, name, description, volume, created_at, updated_at
		FROM wms_inventory.skus
		WHERE sku_id = $1`

	var sku domain.SKU
	err := r.db.QueryRow(ctx, query, skuID).Scan(
		&sku.SKUID,
		&sku.Name,
		&sku.Description,
		&sku.Volume,
		&sku.CreatedAt,
		&sku.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetSKUByID: %w", ErrSKUNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetSKUByID scan: %w", err)
	}

	return &sku, nil
}

func (r *Repository) InsertProduct(ctx context.Context, product *domain.Product) error {
	const query = `
		INSERT INTO wms_inventory.products (
			product_id,
			sku_id,
			shipment_id,
			cargoplace_id,
			box_id,
			qr_code,
			status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := r.db.Exec(
		ctx,
		query,
		product.ProductID,
		product.SKUID,
		product.ShipmentID,
		product.CargoplaceID,
		product.BoxID,
		product.QRCode,
		product.Status,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("receiving.Repository.InsertProduct: %w", ErrQRAlreadyExists)
		}
		return fmt.Errorf("receiving.Repository.InsertProduct exec: %w", err)
	}

	return nil
}

func (r *Repository) CountProductsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wms_inventory.products
		WHERE cargoplace_id = $1`

	var total int
	if err := r.db.QueryRow(ctx, query, cargoplaceID).Scan(&total); err != nil {
		return 0, fmt.Errorf("receiving.Repository.CountProductsByCargoplace scan: %w", err)
	}
	return total, nil
}

func (r *Repository) CountExpectedItemsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error) {
	const query = `
		SELECT COALESCE(SUM(expected_qty), 0)
		FROM wms_inventory.expected_cargoplace_skus
		WHERE cargoplace_id = $1`

	var total int
	if err := r.db.QueryRow(ctx, query, cargoplaceID).Scan(&total); err != nil {
		return 0, fmt.Errorf("receiving.Repository.CountExpectedItemsByCargoplace scan: %w", err)
	}
	return total, nil
}

func (r *Repository) InsertReceivingGateLog(ctx context.Context, params *GateLogParams) error {
	const query = `
		INSERT INTO wms_ops.receiving_gate (
			ttn_code,
			cargoplace_code,
			event_id,
			shipment_id,
			cargoplace_id,
			operator_id,
			action,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := r.db.Exec(
		ctx,
		query,
		params.TTNCode,
		params.CargoplaceCode,
		uuid.New(),
		params.ShipmentID,
		params.CargoplaceID,
		params.OperatorID,
		params.Action,
		params.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("receiving.Repository.InsertReceivingGateLog exec: %w", err)
	}

	return nil
}

func (r *Repository) InsertReceivingTableLog(ctx context.Context, params *TableLogParams) error {
	const query = `
		INSERT INTO wms_ops.receiving_table (
			event_id,
			cargoplace_id,
			box_id,
			operator_id,
			action,
			box_barcode,
			sku_id,
			qr_code,
			product_id,
			buffer_bin_id,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := r.db.Exec(
		ctx,
		query,
		uuid.New(),
		params.CargoplaceID,
		params.BoxID,
		params.OperatorID,
		params.Action,
		params.BoxBarcode,
		params.SKUID,
		params.QRCode,
		params.ProductID,
		params.BufferBinID,
		params.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("receiving.Repository.InsertReceivingTableLog exec: %w", err)
	}

	return nil
}
