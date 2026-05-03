package dispatches

import (
	"context"
	"fmt"

	"wms/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateNewDispatch(ctx context.Context, disp *NewDispatchQuery, dispCode string) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch
	err := r.db.QueryRow(ctx, `
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
`, disp.DestinationId, disp.VehicleNumber, disp.DriverName, disp.DriverPhone, disp.ScheduledAt, dispCode).Scan(
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

func (r *Repository) GetActualDispatchCode(ctx context.Context) (int, error) {
	var dispatchCode int
	err := r.db.QueryRow(ctx, `select count(*) from wms_inventory.outbound_dispatches where created_at >= NOW()::DATE`).Scan(&dispatchCode)
	if err != nil {
		return -1, err
	}
	return dispatchCode, nil
}

func (r *Repository) GetDispatchById(ctx context.Context, disp_id uuid.UUID) (domain.OutboundDispatch, error) {
	var result domain.OutboundDispatch
	err := r.db.QueryRow(ctx, `select dispatch_id,dispatch_code,warehouse_id,destination_id,vehicle_number,driver_name,driver_phone,status,scheduled_at,arrived_at,departed_at,created_at,updated_at from wms_inventory.outbound_dispatches where dispatch_id = $1`, disp_id).Scan(
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
	if filter.DestinationId != uuid.Nil {
		query += fmt.Sprintf(" AND destination_id=$%d", argcount)
		args = append(args, filter.DestinationId)
		argcount++
	}
	if filter.WarehouseId != -1 {
		query += fmt.Sprintf(" AND warehouse_id=$%d", argcount)
		args = append(args, filter.WarehouseId)
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
