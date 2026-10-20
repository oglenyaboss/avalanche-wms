package assembly

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wms/internal/signedops"
)

// Репозиторий подписанной выгрузки корзины в буфер отгрузки (ADR-0001, #14).

// basketPredicate — общее условие «корзины оператора» (предикат прежней атомарной
// выгрузки): товары в статусе ASSEMBLED с DONE-задачей данного оператора под назначение
// буфера. Одно определение для read-only списка черновика и блокирующего SELECT'а submit'а.
const basketPredicate = `
		FROM wms_inventory.products p
		JOIN wms_ops.assembly_tasks t ON t.product_id = p.product_id
		WHERE t.operator_id = $1
		  AND t.status = 'DONE'
		  AND t.destination_id = $2
		  AND p.status = 'ASSEMBLED'`

// ListOperatorBasket возвращает корзину оператора (товар + его заказ) для назначения
// буфера отгрузки — read-only список для черновика подписанного Pick.
func (r *Repository) ListOperatorBasket(ctx context.Context, operatorID, destinationID uuid.UUID) ([]BasketItem, error) {
	return operatorBasket(ctx, r.q, operatorID, destinationID, false)
}

// LockOperatorBasket блокирует строки корзины оператора FOR UPDATE (в порядке product_id).
// Вызывать только на tx-репозитории из WithTx.
func (r *Repository) LockOperatorBasket(ctx context.Context, operatorID, destinationID uuid.UUID) ([]BasketItem, error) {
	return operatorBasket(ctx, r.q, operatorID, destinationID, true)
}

// operatorBasket выполняет один и тот же запрос на пуле или внутри транзакции;
// товар может иметь несколько DONE-задач после перераспределения, поэтому
// дубликаты по product_id схлопываются.
func operatorBasket(ctx context.Context, q dbTX, operatorID, destinationID uuid.UUID, lock bool) ([]BasketItem, error) {
	query := `SELECT p.product_id, p.order_id` + basketPredicate + ` ORDER BY p.product_id`
	if lock {
		query += ` FOR UPDATE`
	}

	rows, err := q.Query(ctx, query, operatorID, destinationID)
	if err != nil {
		return nil, fmt.Errorf("assembly.Repository.operatorBasket query: %w", err)
	}
	defer rows.Close()

	items := make([]BasketItem, 0)
	seen := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var it BasketItem
		// products.order_id is nullable (ON DELETE SET NULL), so scan into a pointer:
		// decoding a SQL NULL into a non-pointer uuid.UUID would error and 500 the call.
		if err := rows.Scan(&it.ProductID, &it.OrderID); err != nil {
			return nil, fmt.Errorf("assembly.Repository.operatorBasket scan: %w", err)
		}
		if _, dup := seen[it.ProductID]; dup {
			continue
		}
		seen[it.ProductID] = struct{}{}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("assembly.Repository.operatorBasket rows: %w", err)
	}
	return items, nil
}

// MoveProductsToShippingBuffer переводит товары корзины в READY_TO_SHIP в ячейке
// буфера отгрузки. Строки уже заблокированы LockOperatorBasket и сверены с черновиком.
func (r *Repository) MoveProductsToShippingBuffer(ctx context.Context, productIDs []uuid.UUID, bufferBinID uuid.UUID) error {
	const query = `
		UPDATE wms_inventory.products
		SET status = 'READY_TO_SHIP', bin_id = $2, updated_at = NOW()
		WHERE product_id = ANY($1)`

	if _, err := r.q.Exec(ctx, query, productIDs, bufferBinID); err != nil {
		return fmt.Errorf("assembly.Repository.MoveProductsToShippingBuffer exec: %w", err)
	}
	return nil
}

// StampTaskUnload проставляет DONE-задачам товаров действие-часть и время выгрузки.
// Guard action_id IS NULL защищает задачи прошлых циклов подбора того же товара
// от перезаписи новым действием.
func (r *Repository) StampTaskUnload(ctx context.Context, actionID uuid.UUID, productIDs []uuid.UUID, operatorID, destinationID uuid.UUID, occurredAt time.Time) error {
	const query = `
		UPDATE wms_ops.assembly_tasks
		SET action_id = $1, unloaded_at = $2, updated_at = NOW()
		WHERE product_id = ANY($3)
		  AND operator_id = $4
		  AND destination_id = $5
		  AND status = 'DONE'
		  AND action_id IS NULL`

	if _, err := r.q.Exec(ctx, query, actionID, occurredAt, productIDs, operatorID, destinationID); err != nil {
		return fmt.Errorf("assembly.Repository.StampTaskUnload exec: %w", err)
	}
	return nil
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
