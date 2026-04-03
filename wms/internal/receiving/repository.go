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

type GateLogParams struct {
	TTNCode        *string
	CargoplaceCode *string
	ShipmentID     *uuid.UUID
	CargoplaceID   *uuid.UUID
	OperatorID     uuid.UUID
	Action         string
	OccurredAt     time.Time
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

func (r *Repository) UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, status string) error {
	const query = `
		UPDATE wms_inventory.inbound_shipments
		SET status = $2
		WHERE shipment_id = $1`

	tag, err := r.q.Exec(ctx, query, shipmentID, status)
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

	tag, err := r.q.Exec(ctx, query, cargoplaceID, status, receivedAt)
	if err != nil {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceReceivedAtGate exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("receiving.Repository.UpdateCargoplaceReceivedAtGate: %w", ErrCargoplaceNotInShipment)
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

	if _, err := r.q.Exec(ctx, query, shipmentID, notReceivedStatus); err != nil {
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
