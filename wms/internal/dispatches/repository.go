package dispatches

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"wms/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repository) CreateNewDispatch(ctx context.Context, disp *NewDispatchQuery) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch
	err := r.WithTx(ctx, func(tx pgx.Tx) error {
		var dispatchCode int
		err := tx.QueryRow(ctx, `select count(*) from wms_inventory.outbound_dispatches where created_at >= NOW()::DATE`).Scan(&dispatchCode)
		if err != nil {
			return err
		}
		dispCode := time.Now().Format("2006-0102")
		dispCode += "-"
		q := strconv.Itoa(dispatchCode + 1)
		for len(q) < 3 {
			q = "0" + q
		}
		dispCode += q
		err = tx.QueryRow(ctx, `
	    INSERT INTO wms_inventory.outbound_dispatches
	        (dispatch_id, destination_id, warehouse_id, vehicle_number,
	         driver_name, driver_phone, scheduled_at, dispatch_code)
	    SELECT
	        gen_random_uuid(),
	        $1,
	        d.warehouse_id,
	        $2,
	        $3,
	        $4,
	        $5,
			$6
	    FROM wms_inventory.destinations d
	    WHERE d.destination_id = $1
		FOR UPDATE
	    RETURNING *
	`, disp.DestinationID, disp.VehicleNumber, disp.DriverName, disp.DriverPhone, disp.ScheduledAt, dispCode).Scan(
			&result.DispatchID,
			&result.DispatchCode,
			&result.WarehouseID,
			&result.DestinationID,
			&result.VehicleNumber,
			&result.DriverName,
			&result.DriverPhone,
			&result.Status,
			&result.ScheduledAt,
			&result.ArrivedAt,
			&result.DepartedAt,
			&result.CreatedAt,
			&result.UpdatedAt,
		)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.OutboundDispatch{}, ErrDestinationNotFound
		}
		return domain.OutboundDispatch{}, err
	}
	return result, nil
}

func (r *Repository) GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch
	err := r.db.QueryRow(ctx, `select dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,arrived_at,departed_at,created_at,updated_at from wms_inventory.outbound_dispatches where dispatch_id = $1`, dispID).Scan(
		&result.DispatchID,
		&result.DispatchCode,
		&result.WarehouseID,
		&result.DestinationID,
		&result.VehicleNumber,
		&result.DriverName,
		&result.DriverPhone,
		&result.Status,
		&result.ScheduledAt,
		&result.ArrivedAt,
		&result.DepartedAt,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		return domain.OutboundDispatch{}, err
	}
	return result, nil
}

func (r *Repository) GetDispatchesByFilter(ctx context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error) {
	query := `select  dispatch_id, dispatch_code, warehouse_id, destination_id,
            vehicle_number, driver_name, driver_phone, status,
            scheduled_at, arrived_at, departed_at, created_at, updated_at
        FROM wms_inventory.outbound_dispatches
        WHERE 1=1`
	argcount := 1
	args := []interface{}{}
	if filter.Status != nil {
		query += fmt.Sprintf(" AND status=$%d", argcount)
		args = append(args, filter.Status)
		argcount++
	}
	if filter.DestinationID != uuid.Nil {
		query += fmt.Sprintf(" AND destination_id=$%d", argcount)
		args = append(args, filter.DestinationID)
		argcount++
	}
	if filter.WarehouseID != -1 {
		query += fmt.Sprintf(" AND warehouse_id=$%d", argcount)
		args = append(args, filter.WarehouseID)
	}
	query += " order by scheduled_at ASC"
	rows, err := r.db.Query(ctx, query, args...)

	if err != nil {
		return nil, fmt.Errorf("failed to query dispatches: %w", err)
	}
	defer rows.Close()

	var dispatches []domain.OutboundDispatch
	for rows.Next() {
		var d domain.OutboundDispatch
		err := rows.Scan(
			&d.DispatchID,
			&d.DispatchCode,
			&d.WarehouseID,
			&d.DestinationID,
			&d.VehicleNumber,
			&d.DriverName,
			&d.DriverPhone,
			&d.Status,
			&d.ScheduledAt,
			&d.ArrivedAt,
			&d.DepartedAt,
			&d.CreatedAt,
			&d.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan dispatch: %w", err)
		}
		dispatches = append(dispatches, d)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return dispatches, nil
}
