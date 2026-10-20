package shipping

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wms/internal/ledger"
	"wms/internal/signedops"
)

// Репозиторий подписанной отгрузки буфера отгрузки на рейс (ADR-0001, #15).

// GetDispatchByID возвращает рейс без блокировки — read-only проверка черновика.
func (r *Repository) GetDispatchByID(ctx context.Context, dispatchID uuid.UUID) (*DispatchRecord, error) {
	const query = `
		SELECT d.dispatch_id, d.dispatch_code, d.destination_id, dest.code, dest.name,
		       d.vehicle_number, d.driver_name, d.driver_phone, d.status, d.arrived_at
		FROM wms_inventory.outbound_dispatches d
		JOIN wms_inventory.destinations dest ON dest.destination_id = d.destination_id
		WHERE d.dispatch_id = $1`

	dispatch, err := r.scanDispatch(ctx, query, dispatchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("shipping.Repository.GetDispatchByID: %w", ErrDispatchNotFound)
		}
		return nil, fmt.Errorf("shipping.Repository.GetDispatchByID scan: %w", err)
	}

	return dispatch, nil
}

// productsForShip выполняет один и тот же запрос на пуле или внутри транзакции;
// spot-режим (непустой productIDs) фильтрует по товару, bulk выбирает всю ячейку.
func productsForShip(ctx context.Context, q dbTX, binID uuid.UUID, productIDs []uuid.UUID, lock bool) ([]ProductForShip, error) {
	query := `
		SELECT product_id, order_id
		FROM wms_inventory.products
		WHERE bin_id = $1 AND status = 'READY_TO_SHIP'`
	args := []any{binID}
	if len(productIDs) > 0 {
		query += ` AND product_id = ANY($2::uuid[])`
		args = append(args, productIDs)
	}
	query += ` ORDER BY created_at`
	if lock {
		query += ` FOR UPDATE`
	}

	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("shipping.Repository.productsForShip query: %w", err)
	}
	defer rows.Close()

	products := make([]ProductForShip, 0)
	for rows.Next() {
		var p ProductForShip
		if err := rows.Scan(&p.ProductID, &p.OrderID); err != nil {
			return nil, fmt.Errorf("shipping.Repository.productsForShip scan: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("shipping.Repository.productsForShip rows: %w", err)
	}

	return products, nil
}

// ListProductsForShip возвращает READY_TO_SHIP товары ячейки буфера (с их заказами)
// — read-only список для черновика подписанной Ship.
func (r *Repository) ListProductsForShip(ctx context.Context, binID uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error) {
	return productsForShip(ctx, r.q, binID, productIDs, false)
}

// LockProductsForShip блокирует строки товаров FOR UPDATE (в порядке created_at).
// Вызывать только на tx-репозитории из WithTx.
func (r *Repository) LockProductsForShip(ctx context.Context, binID uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error) {
	return productsForShip(ctx, r.q, binID, productIDs, true)
}

// UnconfirmedProducts возвращает товары, этап которых (сборка) ещё не подтверждён в цепи.
func (r *Repository) UnconfirmedProducts(ctx context.Context, productIDs []uuid.UUID, aggregate string) ([]uuid.UUID, error) {
	return ledger.UnconfirmedProducts(ctx, r.q, productIDs, aggregate)
}

// LockOperation блокирует все части операции FOR UPDATE (в порядке part_index)
// в текущей транзакции. Вызывать только на tx-репозитории из WithTx.
func (r *Repository) LockOperation(ctx context.Context, operationID, createdBy uuid.UUID) (*signedops.Operation, error) {
	return signedops.LockOperation(ctx, r.tx, operationID, createdBy)
}

// LockSigner берёт FOR SHARE на строку ключа подписанта в текущей транзакции,
// сериализуясь с отзывом ключа (ADR §7). Вызывать только на tx-репозитории из WithTx.
func (r *Repository) LockSigner(ctx context.Context, credentialID []byte) error {
	return signedops.LockSigner(ctx, r.tx, credentialID)
}

// WriteActions пишет по одной outbox-строке на часть и помечает операцию отправленной.
// Вызывать только на tx-репозитории из WithTx.
func (r *Repository) WriteActions(ctx context.Context, op *signedops.Operation, payloads [][]byte) error {
	return signedops.WriteActions(ctx, r.tx, op, payloads)
}
