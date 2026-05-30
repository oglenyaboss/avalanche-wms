package dispatches

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"wms/internal/domain"

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

func (r *Repository) WithTx(ctx context.Context, fn func(dispatchesRepository) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dispatches.Repository.WithTx begin: %w", err)
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
		return fmt.Errorf("dispatches.Repository.WithTx commit: %w", err)
	}

	committed = true
	return nil
}

func (r *Repository) GetActualDispatchCode(ctx context.Context) (int, error) {
	var count int
	err := r.q.QueryRow(ctx, `select count(*) from wms_inventory.outbound_dispatches where created_at >= NOW()::DATE`).Scan(&count)
	if err != nil {
		return -1, err
	}
	return count, nil
}

func (r *Repository) CreateDispatchCode(ctx context.Context) (string, error) {
	count, err := r.GetActualDispatchCode(ctx)
	if err != nil {
		return "", err
	}
	seq := strconv.Itoa(count + 1)
	for len(seq) < 3 {
		seq = "0" + seq
	}
	return "DSP-" + time.Now().Format("2006-0102") + "-" + seq, nil
}

func (r *Repository) CreateNewDispatch(ctx context.Context, disp *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch
	err := r.q.QueryRow(ctx, `
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
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OutboundDispatch{}, ErrDestinationNotFound
		}
		return domain.OutboundDispatch{}, fmt.Errorf("dispatches.Repository.CreateNewDispatch: %w", err)
	}
	return result, nil
}

func (r *Repository) GetDispatchByID(ctx context.Context, dispID uuid.UUID) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch
	err := r.q.QueryRow(ctx, `select dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,arrived_at,departed_at,created_at,updated_at from wms_inventory.outbound_dispatches where dispatch_id = $1`, dispID).Scan(
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
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OutboundDispatch{}, ErrDispatchNotFound
		}
		return domain.OutboundDispatch{}, fmt.Errorf("dispatches.Repository.GetDispatchByID: %w", err)
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
	args := []any{}
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
	rows, err := r.q.Query(ctx, query, args...)

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
