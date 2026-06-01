package destinations

import (
	"context"
	"fmt"

	"wms/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repository is read-only, so it holds only the query handle (no *pgxpool.Pool
// for transactions — there is no WithTx here, unlike the write-capable modules).
type Repository struct {
	q dbTX
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{q: db}
}

// GetDestinations возвращает магазины-получатели, отсортированные по code ASC.
// warehouseID <= 0 трактуется как «без фильтра» (вернуть все склады); любое
// положительное значение фильтрует по warehouse_id.
func (r *Repository) GetDestinations(ctx context.Context, warehouseID int64) ([]domain.Destination, error) {
	query := `SELECT destination_id, code, name, address, warehouse_id
        FROM wms_inventory.destinations`
	args := []any{}
	if warehouseID > 0 {
		query += ` WHERE warehouse_id = $1`
		args = append(args, warehouseID)
	}
	// Bounded read: destinations is a small reference table; the cap guards
	// against an unbounded scan if it ever grows unexpectedly.
	query += ` ORDER BY code ASC LIMIT 1000`

	rows, err := r.q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("destinations.Repository.GetDestinations query: %w", err)
	}
	defer rows.Close()

	var destinations []domain.Destination
	for rows.Next() {
		var d domain.Destination
		if err := rows.Scan(
			&d.DestinationID,
			&d.Code,
			&d.Name,
			&d.Address,
			&d.WarehouseID,
		); err != nil {
			return nil, fmt.Errorf("destinations.Repository.GetDestinations scan: %w", err)
		}
		destinations = append(destinations, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("destinations.Repository.GetDestinations rows: %w", err)
	}
	return destinations, nil
}
