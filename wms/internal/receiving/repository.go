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

func (r *Repository) WithTx(ctx context.Context, fn func(receivingRepository) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("receiving.Repository.WithTx begin: %w", err)
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
		return fmt.Errorf("receiving.Repository.WithTx commit: %w", err)
	}

	committed = true
	return nil
}

// GetShipmentByTTN retrieves an inbound shipment from the database based on the provided TTN code.
func (r *Repository) GetShipmentByTTN(ctx context.Context, ttnCode string) (*domain.InboundShipment, error) {
	const query = `
		SELECT shipment_id, warehouse_id, ttn_code, status, created_at, updated_at
		FROM wms_inventory.inbound_shipments
		WHERE ttn_code = $1`

	var shipment domain.InboundShipment
	err := r.q.QueryRow(ctx, query, ttnCode).Scan(
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
	err := r.q.QueryRow(ctx, query, shipmentID).Scan(
		&shipment.ShipmentID,
		&shipment.WarehouseID,
		&shipment.TTNCode,
		&shipment.Status,
		&shipment.CreatedAt,
		&shipment.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetShipmentByID: %w", ErrShipmentNotFound)
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

	rows, err := r.q.Query(ctx, query, shipmentID)
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
	err := r.q.QueryRow(ctx, query, shipmentID, cargoplaceCode).Scan(
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
	err := r.q.QueryRow(ctx, query, cargoplaceID).Scan(
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

func (r *Repository) GetCargoplaceByIDForUpdate(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error) {
	const query = `
		SELECT cargoplace_id, shipment_id, cargoplace_code, status, received_at_gate_at, created_at, updated_at
		FROM wms_inventory.cargoplaces
		WHERE cargoplace_id = $1
		FOR UPDATE`

	var cp domain.Cargoplace
	err := r.q.QueryRow(ctx, query, cargoplaceID).Scan(
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
			return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByIDForUpdate: %w", ErrCargoplaceNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetCargoplaceByIDForUpdate scan: %w", err)
	}

	return &cp, nil
}

func (r *Repository) UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, newStatus, expectedStatus string) error {
	const query = `
		UPDATE wms_inventory.inbound_shipments
		SET status = $2
		WHERE shipment_id = $1 AND status = $3`

	tag, err := r.q.Exec(ctx, query, shipmentID, newStatus, expectedStatus)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateShipmentStatus exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateShipmentStatus: %w", ErrShipmentNotFound)
	}

	return nil
}

func (r *Repository) UpdateCargoplaceReceivedAtGate(
	ctx context.Context,
	cargoplaceID uuid.UUID,
	newStatus, expectedStatus string,
	receivedAt time.Time,
) error {
	const query = `
		UPDATE wms_inventory.cargoplaces
		SET status = $2, received_at_gate_at = $3
		WHERE cargoplace_id = $1 AND status = $4`

	tag, err := r.q.Exec(ctx, query, cargoplaceID, newStatus, receivedAt, expectedStatus)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceReceivedAtGate exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceReceivedAtGate: %w", ErrCargoplaceAlreadyReceived)
	}

	return nil
}

func (r *Repository) UpdateCargoplaceStatus(ctx context.Context, cargoplaceID uuid.UUID, newStatus, expectedStatus string) error {
	const query = `
		UPDATE wms_inventory.cargoplaces
		SET status = $2
		WHERE cargoplace_id = $1 AND status = $3`

	tag, err := r.q.Exec(ctx, query, cargoplaceID, newStatus, expectedStatus)
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
		WHERE shipment_id = $1 AND status = $3`

	if _, err := r.q.Exec(ctx, query, shipmentID, notReceivedStatus, cargoplaceStatusExpected); err != nil {
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
	if err := r.q.QueryRow(ctx, query, shipmentID).Scan(&total); err != nil {
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
	if err := r.q.QueryRow(ctx, query, shipmentID, status).Scan(&total); err != nil {
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

	rows, err := r.q.Query(ctx, query, cargoplaceID)
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
		WHERE wms_inventory.boxes.status != 'CLOSED'
		RETURNING box_id, cargoplace_id, box_barcode, status, created_at, updated_at`

	var box domain.Box
	err := r.q.QueryRow(ctx, query, uuid.New(), cargoplaceID, boxBarcode, status).Scan(
		&box.BoxID,
		&box.CargoplaceID,
		&box.BoxBarcode,
		&box.Status,
		&box.CreatedAt,
		&box.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ErrNoRows здесь возможен только при ON CONFLICT DO UPDATE WHERE status != 'CLOSED'
			// — то есть существующий box в статусе CLOSED. Проверяем явно, чтобы не маппить
			// слепо все ErrNoRows в BoxNotOpen (защита от будущих изменений в SQL).
			existing, getErr := r.GetBoxByCargoplaceAndBarcode(ctx, cargoplaceID, boxBarcode)
			if getErr != nil {
				return nil, fmt.Errorf("receiving.Repository.UpsertBox verify state: %w", getErr)
			}
			if existing.Status == boxStatusClosed {
				return nil, fmt.Errorf("receiving.Repository.UpsertBox: %w", ErrBoxNotOpen)
			}
			return nil, fmt.Errorf("receiving.Repository.UpsertBox: unexpected no rows with box status %s", existing.Status)
		}
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
	err := r.q.QueryRow(ctx, query, boxID).Scan(
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
	err := r.q.QueryRow(ctx, query, cargoplaceID, boxBarcode).Scan(
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

	tag, err := r.q.Exec(ctx, query, boxID, status)
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
	if err := r.q.QueryRow(ctx, query, boxID).Scan(&total); err != nil {
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
	err := r.q.QueryRow(ctx, query, barcode).Scan(
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
	err := r.q.QueryRow(ctx, query, skuID).Scan(
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

	_, err := r.q.Exec(
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

func (r *Repository) GetBinByID(ctx context.Context, binID uuid.UUID) (*domain.Bin, error) {
	const query = `
		SELECT bin_id, warehouse_id, code, section, volume, created_at, updated_at
		FROM wms_inventory.bins
		WHERE bin_id = $1`

	var bin domain.Bin
	err := r.q.QueryRow(ctx, query, binID).Scan(
		&bin.BinID,
		&bin.WarehouseID,
		&bin.Code,
		&bin.Section,
		&bin.Volume,
		&bin.CreatedAt,
		&bin.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.GetBinByID: %w", ErrBinNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.GetBinByID scan: %w", err)
	}

	return &bin, nil
}

func (r *Repository) ScanBufferWithLog(
	ctx context.Context,
	cargoplaceID uuid.UUID,
	bufferBinID uuid.UUID,
	logParams *TableLogParams,
) (int, error) {
	// Перемещаем только продукты из закрытых boxes — flow требует close-box перед scan-buffer.
	// Без этого фильтра OPEN box с частично заполненными QR'ами тоже попадёт в буфер.
	const moveQuery = `
		UPDATE wms_inventory.products p
		SET bin_id = $2, updated_at = now()
		FROM wms_inventory.boxes b
		WHERE p.box_id = b.box_id
		  AND p.cargoplace_id = $1
		  AND p.status = 'RECEIVED'
		  AND b.status = 'CLOSED'`

	tag, err := r.q.Exec(ctx, moveQuery, cargoplaceID, bufferBinID)
	if err != nil {
		return 0, fmt.Errorf("receiving.Repository.ScanBufferWithLog move products: %w", err)
	}

	if err := r.InsertReceivingTableLog(ctx, logParams); err != nil {
		return 0, fmt.Errorf("receiving.Repository.ScanBufferWithLog insert log: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

func (r *Repository) CountProductsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM wms_inventory.products
		WHERE cargoplace_id = $1`

	var total int
	if err := r.q.QueryRow(ctx, query, cargoplaceID).Scan(&total); err != nil {
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
	if err := r.q.QueryRow(ctx, query, cargoplaceID).Scan(&total); err != nil {
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

	_, err := r.q.Exec(
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

	_, err := r.q.Exec(
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

// CloseCargoplaceWithOutbox
// CloseCargoplaceWithOutbox: закрытие cargoplace + продукты + outbox в одной транзакции.
//
// ВНИМАНИЕ: метод сам управляет транзакцией через r.db.Begin — НЕ оборачивать в WithTx.
// При оборачивании внешняя tx и внутренний Begin возьмут разные коннекты из пула,
// нарушится изоляция, FOR UPDATE'ы и видимость промежуточных записей сломаются.
func (r *Repository) CloseCargoplaceWithOutbox(
	ctx context.Context,
	params *CloseCargoplaceParams,
) (*CloseCargoplaceTxResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var cpStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM wms_inventory.cargoplaces WHERE cargoplace_id = $1 FOR UPDATE`,
		params.CargoplaceID).Scan(&cpStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox: %w", ErrCargoplaceNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox lock cargoplace: %w", err)
	}
	if cpStatus != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox: %w", ErrCargoplaceNotInProgress)
	}

	const updateCargoplaceQuery = `
		UPDATE wms_inventory.cargoplaces
		SET status = $2, updated_at = now()
		WHERE cargoplace_id = $1 AND status = $3`

	tag, err := tx.Exec(
		ctx,
		updateCargoplaceQuery,
		params.CargoplaceID,
		cargoplaceStatusTableClosed,
		cargoplaceStatusTableInProgress,
	)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox update cargoplace: %w", err)
	}
	if tag.RowsAffected() == 0 {
		exists, err := r.cargoplaceExistsTx(ctx, tx, params.CargoplaceID)
		if err != nil {
			return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox check cargoplace existence: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox: %w", ErrCargoplaceNotFound)
		}
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox: %w", ErrCargoplaceNotInProgress)
	}

	expectedSKUs, err := r.listExpectedSKUsByCargoplaceTx(ctx, tx, params.CargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox list expected skus: %w", err)
	}

	receivedSKUCounts, err := r.listReceivedProductCountsBySKUTx(ctx, tx, params.CargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox list received skus: %w", err)
	}

	if err := r.insertReceivingTableLogTx(ctx, tx, &TableLogParams{
		CargoplaceID: params.CargoplaceID,
		OperatorID:   params.OperatorID,
		Action:       "CLOSE_CARGO",
		OccurredAt:   params.OccurredAt,
	}); err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox insert log: %w", err)
	}

	productIDs, err := r.listProductIDsByCargoplaceTx(ctx, tx, params.CargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox list product ids: %w", err)
	}

	if err := r.insertOutboxEventsTx(ctx, tx, params.CargoplaceID, productIDs); err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox insert outbox events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("receiving.Repository.CloseCargoplaceWithOutbox commit tx: %w", err)
	}

	return &CloseCargoplaceTxResult{
		ExpectedSKUs:        expectedSKUs,
		ReceivedSKUCounts:   receivedSKUCounts,
		OutboxEventsCreated: len(productIDs),
	}, nil
}

func (r *Repository) insertReceivingTableLogTx(
	ctx context.Context,
	tx pgx.Tx,
	params *TableLogParams,
) error {
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

	_, err := tx.Exec(
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
		return fmt.Errorf("receiving.Repository.insertReceivingTableLogTx exec: %w", err)
	}

	return nil
}

func (r *Repository) listProductIDsByCargoplaceTx(
	ctx context.Context,
	tx pgx.Tx,
	cargoplaceID uuid.UUID,
) ([]uuid.UUID, error) {
	const query = `
		SELECT product_id
		FROM wms_inventory.products
		WHERE cargoplace_id = $1
			AND status = 'RECEIVED'
		ORDER BY product_id`

	rows, err := tx.Query(ctx, query, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.listProductIDsByCargoplaceTx query: %w", err)
	}
	defer rows.Close()

	productIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var productID uuid.UUID
		if err := rows.Scan(&productID); err != nil {
			return nil, fmt.Errorf("receiving.Repository.listProductIDsByCargoplaceTx scan: %w", err)
		}
		productIDs = append(productIDs, productID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("receiving.Repository.listProductIDsByCargoplaceTx rows: %w", err)
	}

	return productIDs, nil
}

func (r *Repository) cargoplaceExistsTx(
	ctx context.Context,
	tx pgx.Tx,
	cargoplaceID uuid.UUID,
) (bool, error) {
	const query = `
		SELECT EXISTS(
			SELECT 1
			FROM wms_inventory.cargoplaces
			WHERE cargoplace_id = $1
		)`

	var exists bool
	if err := tx.QueryRow(ctx, query, cargoplaceID).Scan(&exists); err != nil {
		return false, fmt.Errorf("receiving.Repository.cargoplaceExistsTx scan: %w", err)
	}

	return exists, nil
}

func (r *Repository) listExpectedSKUsByCargoplaceTx(
	ctx context.Context,
	tx pgx.Tx,
	cargoplaceID uuid.UUID,
) ([]ExpectedSKU, error) {
	const query = `
		SELECT ecs.sku_id, s.name, ecs.expected_qty
		FROM wms_inventory.expected_cargoplace_skus ecs
		JOIN wms_inventory.skus s ON s.sku_id = ecs.sku_id
		WHERE ecs.cargoplace_id = $1
		ORDER BY s.name`

	rows, err := tx.Query(ctx, query, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.listExpectedSKUsByCargoplaceTx query: %w", err)
	}
	defer rows.Close()

	items := make([]ExpectedSKU, 0)
	for rows.Next() {
		var item ExpectedSKU
		if err := rows.Scan(&item.SKUID, &item.SKUName, &item.ExpectedQty); err != nil {
			return nil, fmt.Errorf("receiving.Repository.listExpectedSKUsByCargoplaceTx scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("receiving.Repository.listExpectedSKUsByCargoplaceTx rows: %w", err)
	}

	return items, nil
}

func (r *Repository) listReceivedProductCountsBySKUTx(
	ctx context.Context,
	tx pgx.Tx,
	cargoplaceID uuid.UUID,
) ([]ReceivedSKUCount, error) {
	const query = `
		-- Count actually created products in the cargoplace, grouped by SKU, for shortage reconciliation.
		SELECT p.sku_id, s.name, COUNT(*)::int AS received_qty
		FROM wms_inventory.products p
		JOIN wms_inventory.skus s ON s.sku_id = p.sku_id
		WHERE p.cargoplace_id = $1 AND p.status = 'RECEIVED'
		GROUP BY p.sku_id, s.name
		ORDER BY s.name`

	rows, err := tx.Query(ctx, query, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Repository.listReceivedProductCountsBySKUTx query: %w", err)
	}
	defer rows.Close()

	items := make([]ReceivedSKUCount, 0)
	for rows.Next() {
		var item ReceivedSKUCount
		if err := rows.Scan(&item.SKUID, &item.SKUName, &item.ReceivedQty); err != nil {
			return nil, fmt.Errorf("receiving.Repository.listReceivedProductCountsBySKUTx scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("receiving.Repository.listReceivedProductCountsBySKUTx rows: %w", err)
	}

	return items, nil
}

// insertOutboxEventsTx вставляет outbox-события для всех продуктов закрываемого cargoplace.
//
// Защита от дублей сейчас обеспечивается на уровне UPDATE wms_inventory.cargoplaces
// в CloseCargoplaceWithOutbox: повторный вызов второй раз не пройдёт WHERE status='TABLE_IN_PROGRESS'
// и метод вернёт ErrCargoplaceNotInProgress до этого INSERT'а. Поэтому отдельный
// ON CONFLICT здесь не нужен — event_id всегда новый (uuid.New()).
//
// Если в будущем появится worker-level retry (с тем же payload), потребуется
// unique constraint на (aggregate_id, payload_hash) в public.outbox_events
// и соответствующий ON CONFLICT — это вне scope текущей задачи.
func (r *Repository) insertOutboxEventsTx(
	ctx context.Context,
	tx pgx.Tx,
	cargoplaceID uuid.UUID,
	productIDs []uuid.UUID,
) error {
	if len(productIDs) == 0 {
		return nil
	}

	eventIDs := make([]uuid.UUID, 0, len(productIDs))
	aggregateIDs := make([]uuid.UUID, 0, len(productIDs))
	payloadHashes := make([]string, 0, len(productIDs))

	for _, productID := range productIDs {
		payloadHash, err := payloadHashForReceiving(productID, cargoplaceID)
		if err != nil {
			return fmt.Errorf("receiving.Repository.insertOutboxEventsTx build payload hash: %w", err)
		}

		eventIDs = append(eventIDs, uuid.New())
		aggregateIDs = append(aggregateIDs, productID)
		payloadHashes = append(payloadHashes, payloadHash)
	}

	const query = `
		INSERT INTO public.outbox_events (
			event_id,
			aggregate_id,
			aggregate_type,
			event_type,
			payload_hash
		)
		SELECT
			event_id,
			aggregate_id,
			'receiving',
			'wms.receiving.v1',
			payload_hash
		FROM unnest($1::uuid[], $2::uuid[], $3::text[]) AS events(event_id, aggregate_id, payload_hash)`

	if _, err := tx.Exec(ctx, query, eventIDs, aggregateIDs, payloadHashes); err != nil {
		return fmt.Errorf("receiving.Repository.insertOutboxEventsTx exec: %w", err)
	}

	return nil
}
