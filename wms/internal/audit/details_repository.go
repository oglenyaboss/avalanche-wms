package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wms/internal/signedaction"
)

// Details recomputes the detailsHash inputs of the action from the domain tables it changed (ADR §4).
func (r *Repository) Details(ctx context.Context, a Action) (Details, error) {
	switch a.ActionType {
	case signedaction.TypeReceive:
		return r.receiveDetails(ctx, a)
	case signedaction.TypePutAway:
		return r.putAwayDetails(ctx, a)
	case signedaction.TypePick:
		return r.pickDetails(ctx, a)
	case signedaction.TypeShip:
		return r.shipDetails(ctx, a)
	default:
		return Details{}, fmt.Errorf("audit.Repository.Details: %w %d", ErrUnknownActionType, a.ActionType)
	}
}

// detailRow is the single DISTINCT (entity, operator, occurred_at) row of one detail query;
// EntityID is the cargoplace, storage bin or dispatch depending on the action type.
type detailRow struct {
	EntityID   uuid.UUID
	OperatorID uuid.UUID
	OccurredAt time.Time
}

// pickRow is the single DISTINCT (operator, unloaded_at) row of the pick query.
type pickRow struct {
	OperatorID uuid.UUID
	OccurredAt *time.Time
}

func (r *Repository) receiveDetails(ctx context.Context, a Action) (Details, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT cargoplace_id, operator_id, occurred_at
		FROM wms_ops.receiving_table
		WHERE operation_id = $1 AND action = 'CLOSE_CARGO'`, a.OperationID)
	if err != nil {
		return Details{}, fmt.Errorf("audit.Repository.Details receiving_table: %w", err)
	}
	defer rows.Close()
	row, err := pgx.CollectExactlyOneRow(rows, rowToDetail)
	if err != nil {
		return Details{}, detailError("receiving_table", err)
	}
	return Details{
		ActionType:   a.ActionType,
		Salt:         a.Salt,
		CargoplaceID: row.EntityID,
		OperatorID:   row.OperatorID,
		OccurredAtMs: uint64(row.OccurredAt.UnixMilli()),
	}, nil
}

func (r *Repository) putAwayDetails(ctx context.Context, a Action) (Details, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT bin_id, operator_id, occurred_at
		FROM wms_ops.putaways
		WHERE action_id = $1`, a.ActionID)
	if err != nil {
		return Details{}, fmt.Errorf("audit.Repository.Details putaways: %w", err)
	}
	defer rows.Close()
	row, err := pgx.CollectExactlyOneRow(rows, rowToDetail)
	if err != nil {
		return Details{}, detailError("putaways", err)
	}
	return Details{
		ActionType:   a.ActionType,
		Salt:         a.Salt,
		StorageBinID: row.EntityID,
		OperatorID:   row.OperatorID,
		OccurredAtMs: uint64(row.OccurredAt.UnixMilli()),
	}, nil
}

func (r *Repository) pickDetails(ctx context.Context, a Action) (Details, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT operator_id, unloaded_at
		FROM wms_ops.assembly_tasks
		WHERE action_id = $1`, a.ActionID)
	if err != nil {
		return Details{}, fmt.Errorf("audit.Repository.Details assembly_tasks: %w", err)
	}
	defer rows.Close()
	row, err := pgx.CollectExactlyOneRow(rows, rowToPick)
	if err != nil {
		return Details{}, detailError("assembly_tasks", err)
	}
	if row.OccurredAt == nil {
		return Details{}, fmt.Errorf("audit.Repository.Details assembly_tasks: %w: unloaded_at is null", ErrDomainRowMissing)
	}

	orderRows, err := r.db.Query(ctx, `
		SELECT DISTINCT order_id
		FROM wms_ops.assembly_tasks
		WHERE action_id = $1`, a.ActionID)
	if err != nil {
		return Details{}, fmt.Errorf("audit.Repository.Details assembly_tasks: %w", err)
	}
	defer orderRows.Close()
	orders, err := collectUUIDs(orderRows, "assembly_tasks")
	if err != nil {
		return Details{}, err
	}

	binID, err := r.shippingBufferBin(ctx, a)
	if err != nil {
		return Details{}, err
	}
	return Details{
		ActionType:          a.ActionType,
		Salt:                a.Salt,
		ShippingBufferBinID: binID,
		OrderIDs:            orders,
		OperatorID:          row.OperatorID,
		OccurredAtMs:        uint64(row.OccurredAt.UnixMilli()),
	}, nil
}

func (r *Repository) shipDetails(ctx context.Context, a Action) (Details, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT dispatch_id, operator_id, occurred_at
		FROM wms_ops.shippings
		WHERE action_id = $1`, a.ActionID)
	if err != nil {
		return Details{}, fmt.Errorf("audit.Repository.Details shippings: %w", err)
	}
	defer rows.Close()
	row, err := pgx.CollectExactlyOneRow(rows, rowToDetail)
	if err != nil {
		return Details{}, detailError("shippings", err)
	}

	orderRows, err := r.db.Query(ctx, `
		SELECT DISTINCT order_id
		FROM wms_inventory.products
		WHERE product_id = ANY($1) AND order_id IS NOT NULL`, a.ItemIDs)
	if err != nil {
		return Details{}, fmt.Errorf("audit.Repository.Details products: %w", err)
	}
	defer orderRows.Close()
	orders, err := collectUUIDs(orderRows, "products")
	if err != nil {
		return Details{}, err
	}

	binID, err := r.shippingBufferBin(ctx, a)
	if err != nil {
		return Details{}, err
	}
	return Details{
		ActionType:          a.ActionType,
		Salt:                a.Salt,
		DispatchID:          row.EntityID,
		ShippingBufferBinID: binID,
		OrderIDs:            orders,
		OperatorID:          row.OperatorID,
		OccurredAtMs:        uint64(row.OccurredAt.UnixMilli()),
	}, nil
}

// shippingBufferBin reads the single distinct bin_id of the action's items.
func (r *Repository) shippingBufferBin(ctx context.Context, a Action) (uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT bin_id
		FROM wms_inventory.products
		WHERE product_id = ANY($1)`, a.ItemIDs)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("audit.Repository.Details products: %w", err)
	}
	defer rows.Close()
	bins, err := pgx.CollectRows(rows, pgx.RowTo[*uuid.UUID])
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("audit.Repository.Details products: %w", err)
	}
	for _, b := range bins {
		if b == nil {
			return uuid.UUID{}, fmt.Errorf("audit.Repository.Details products: %w: bin_id of an item is null", ErrDomainRowMissing)
		}
	}
	switch {
	case len(bins) == 0:
		return uuid.UUID{}, fmt.Errorf("audit.Repository.Details products: %w", ErrDomainRowMissing)
	case len(bins) > 1:
		return uuid.UUID{}, fmt.Errorf("audit.Repository.Details products: %w", ErrDomainInconsistent)
	}
	return *bins[0], nil
}

// collectUUIDs requires at least one row and collects the single uuid column into a slice.
func collectUUIDs(rows pgx.Rows, table string) ([]uuid.UUID, error) {
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("audit.Repository.Details %s: %w", table, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("audit.Repository.Details %s: %w", table, ErrDomainRowMissing)
	}
	return ids, nil
}

func detailError(table string, err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("audit.Repository.Details %s: %w", table, ErrDomainRowMissing)
	case errors.Is(err, pgx.ErrTooManyRows):
		return fmt.Errorf("audit.Repository.Details %s: %w", table, ErrDomainInconsistent)
	default:
		return fmt.Errorf("audit.Repository.Details %s: %w", table, err)
	}
}

func rowToDetail(row pgx.CollectableRow) (detailRow, error) {
	var d detailRow
	err := row.Scan(&d.EntityID, &d.OperatorID, &d.OccurredAt)
	return d, err
}

func rowToPick(row pgx.CollectableRow) (pickRow, error) {
	var p pickRow
	err := row.Scan(&p.OperatorID, &p.OccurredAt)
	return p, err
}
