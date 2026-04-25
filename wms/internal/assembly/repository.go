package assembly

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	q  dbTX
}

type dbTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db, q: db}
}

func (r *Repository) WithTx(ctx context.Context, fn func(assemblyRepository) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("assembly.Repository.WithTx begin: %w", err)
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
		return fmt.Errorf("assembly.Repository.WithTx commit: %w", err)
	}

	committed = true
	return nil
}

// GetOrdersByDestinationForUpdate возвращает NEW-заказы для магазина с блокировкой (те, которые сделаны данным магазином - destinationID)
func (r *Repository) GetOrdersByDestinationForUpdate(ctx context.Context, destinationID uuid.UUID) ([]domain.Order, error) {
	const query = `
		SELECT order_id, external_order_no, status
		FROM wms_inventory.orders
		WHERE customer_id = $1 AND status = 'NEW'
		FOR UPDATE`

	rows, err := r.q.Query(ctx, query, destinationID)
	if err != nil {
		return nil, fmt.Errorf("assembly.Repository.GetOrdersByDestinationForUpdate query: %w", err)
	}
	defer rows.Close()

	orders := make([]domain.Order, 0)
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(&o.OrderID, &o.ExternalOrderNo, &o.Status); err != nil {
			return nil, fmt.Errorf("assembly.Repository.GetOrdersByDestinationForUpdate scan: %w", err)
		}
		orders = append(orders, o)
	}
	return orders, nil
}

// GetOrderLinesByOrderID возвращает строки заказа
// (по id заказа возвращает все товары (идентификаторы sku_id) и их количество (quantity))
func (r *Repository) GetOrderLinesByOrderID(ctx context.Context, orderID uuid.UUID) ([]domain.OrderLine, error) {
	const query = `
		SELECT id, sku_id, qty
		FROM wms_inventory.order_lines
		WHERE order_id = $1`

	rows, err := r.q.Query(ctx, query, orderID)
	if err != nil {
		return nil, fmt.Errorf("assembly.Repository.GetOrderLinesByOrderID query: %w", err)
	}
	defer rows.Close()

	lines := make([]domain.OrderLine, 0)
	for rows.Next() {
		var l domain.OrderLine
		if err := rows.Scan(&l.ID, &l.SKUID, &l.Qty); err != nil {
			return nil, fmt.Errorf("assembly.Repository.GetOrderLinesByOrderID scan: %w", err)
		}
		l.OrderID = orderID
		lines = append(lines, l)
	}
	return lines, nil
}

// GetAllocateProductsForSKU возвращает STORED продукты для SKU с FIFO и SKIP LOCKED
// (принцип возврата: сначала берем те, которые долго лежат, пропускаем заблокированные другими транзакциями)
func (r *Repository) GetAllocateProductsForSKU(ctx context.Context, skuID uuid.UUID, limit int) ([]domain.Product, error) {
	const query = `
		SELECT product_id, bin_id
		FROM wms_inventory.products
		WHERE sku_id = $1 AND status = 'STORED' AND order_id IS NULL
		ORDER BY created_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED`

	rows, err := r.q.Query(ctx, query, skuID, limit)
	if err != nil {
		return nil, fmt.Errorf("assembly.Repository.GetAllocateProductsForSKU query: %w", err)
	}
	defer rows.Close()

	products := make([]domain.Product, 0)
	for rows.Next() {
		var p domain.Product
		if err := rows.Scan(&p.ProductID, &p.BinID); err != nil {
			return nil, fmt.Errorf("assembly.Repository.GetAllocateProductsForSKU scan: %w", err)
		}
		products = append(products, p)
	}
	return products, nil
}

// GetSKUByID возвращает SKU по ID
func (r *Repository) GetSKUByID(ctx context.Context, skuID uuid.UUID) (*domain.SKU, error) {
	const query = `
		SELECT sku_id, name
		FROM wms_inventory.skus
		WHERE sku_id = $1`

	var sku domain.SKU
	err := r.q.QueryRow(ctx, query, skuID).Scan(&sku.SKUID, &sku.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInsufficientStock
		}
		return nil, fmt.Errorf("assembly.Repository.GetSKUByID scan: %w", err)
	}
	return &sku, nil
}

// UpdateProductAllocated обновляет статус товара на ALLOCATED и привязывает к заказу
func (r *Repository) UpdateProductAllocated(ctx context.Context, productID, orderID uuid.UUID) error {
	const query = `
		UPDATE wms_inventory.products
		SET status = 'ALLOCATED', order_id = $2, updated_at = NOW()
		WHERE product_id = $1 AND status = 'STORED'`

	tag, err := r.q.Exec(ctx, query, productID, orderID)
	if err != nil {
		return fmt.Errorf("assembly.Repository.UpdateProductAllocated exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotAllocated
	}
	return nil
}

// GetBinSectionByID возвращает секцию ячейки (чтобы знать, где искать товар)
func (r *Repository) GetBinSectionByID(ctx context.Context, binID uuid.UUID) (string, error) {
	const query = `
		SELECT section
		FROM wms_inventory.bins
		WHERE bin_id = $1`

	var section string
	err := r.q.QueryRow(ctx, query, binID).Scan(&section)
	if err != nil {
		return "", fmt.Errorf("assembly.Repository.GetBinSectionByID scan: %w", err)
	}
	return section, nil
}

// BatchInsertAssemblyTasks создает tasks для аллоцированных товаров
func (r *Repository) BatchInsertAssemblyTasks(ctx context.Context, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}

	eventIDs := make([]uuid.UUID, len(tasks))
	orderIDs := make([]uuid.UUID, len(tasks))
	productIDs := make([]uuid.UUID, len(tasks))
	skuIDs := make([]uuid.UUID, len(tasks))
	fromBinIDs := make([]uuid.UUID, len(tasks))
	sections := make([]string, len(tasks))
	occurredAts := make([]time.Time, len(tasks))

	for i := range tasks {
		t := &tasks[i]
		eventIDs[i] = t.EventID
		orderIDs[i] = t.OrderID
		productIDs[i] = t.ProductID
		skuIDs[i] = t.SKUID
		fromBinIDs[i] = t.FromBinID
		sections[i] = t.Section
		occurredAts[i] = t.OccurredAt
	}

	const query = `
		INSERT INTO wms_ops.assembly_tasks (
			event_id, order_id, product_id, sku_id, from_bin_id, section,
			status, occurred_at, created_at
		)
		SELECT
			event_id, order_id, product_id, sku_id, from_bin_id, section,
			'PENDING', occurred_at, NOW()
		FROM unnest($1::uuid[], $2::uuid[], $3::uuid[], $4::uuid[], $5::uuid[], $6::text[], $7::timestamptz[])
		AS tasks(event_id, order_id, product_id, sku_id, from_bin_id, section, occurred_at)`

	_, err := r.q.Exec(ctx, query, eventIDs, orderIDs, productIDs, skuIDs, fromBinIDs, sections, occurredAts)
	if err != nil {
		return fmt.Errorf("assembly.Repository.BatchInsertAssemblyTasks exec: %w", err)
	}
	return nil
}

// UpdateOrderStatus обновляет статус заказа
func (r *Repository) UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, status string) error {
	const query = `
		UPDATE wms_inventory.orders
		SET status = $2, updated_at = NOW()
		WHERE order_id = $1`

	tag, err := r.q.Exec(ctx, query, orderID, status)
	if err != nil {
		return fmt.Errorf("assembly.Repository.UpdateOrderStatus exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOrderNotNew
	}
	return nil
}

// GetPendingTaskByProductForUpdate возвращает PENDING задачу для товара с блокировкой
func (r *Repository) GetPendingTaskByProductForUpdate(ctx context.Context, productID uuid.UUID) (*Task, error) {
	const query = `
		SELECT event_id, order_id, product_id, sku_id, from_bin_id, section, status
		FROM wms_ops.assembly_tasks
		WHERE product_id = $1 AND status = 'PENDING'
		FOR UPDATE`

	var t Task
	err := r.q.QueryRow(ctx, query, productID).Scan(
		&t.EventID, &t.OrderID, &t.ProductID, &t.SKUID, &t.FromBinID, &t.Section, &t.Status,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoTaskForProduct
		}
		return nil, fmt.Errorf("assembly.Repository.GetPendingTaskByProductForUpdate scan: %w", err)
	}
	return &t, nil
}

// GetProductByIDForUpdate возвращает товар с блокировкой
func (r *Repository) GetProductByIDForUpdate(ctx context.Context, productID uuid.UUID) (*domain.Product, error) {
	const query = `
		SELECT product_id, status, order_id
		FROM wms_inventory.products
		WHERE product_id = $1
		FOR UPDATE`

	var p domain.Product
	err := r.q.QueryRow(ctx, query, productID).Scan(&p.ProductID, &p.Status, &p.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotAllocated
		}
		return nil, fmt.Errorf("assembly.Repository.GetProductByIDForUpdate scan: %w", err)
	}
	return &p, nil
}

// MarkTaskDone отмечает задачу как выполненную
func (r *Repository) MarkTaskDone(ctx context.Context, eventID, operatorID uuid.UUID) error {
	const query = `
		UPDATE wms_ops.assembly_tasks
		SET status = 'DONE', operator_id = $2, onchain_status = 'PENDING_ONCHAIN',
		    occurred_at = NOW(), updated_at = NOW()
		WHERE event_id = $1`

	tag, err := r.q.Exec(ctx, query, eventID, operatorID)
	if err != nil {
		return fmt.Errorf("assembly.Repository.MarkTaskDone exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoTaskForProduct
	}
	return nil
}

// SetProductAssembled обновляет статус товара на ASSEMBLED (физически взят оператором)
func (r *Repository) SetProductAssembled(ctx context.Context, productID uuid.UUID) error {
	const query = `
		UPDATE wms_inventory.products
		SET status = 'ASSEMBLED', updated_at = NOW()
		WHERE product_id = $1 AND status = 'ALLOCATED'`

	tag, err := r.q.Exec(ctx, query, productID)
	if err != nil {
		return fmt.Errorf("assembly.Repository.SetProductAssembled exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotAllocated
	}
	return nil
}

// InsertPickOutboxEvent создает outbox событие для pick
func (r *Repository) InsertPickOutboxEvent(ctx context.Context, productID uuid.UUID) error {
	eventID := uuid.New()
	payloadHash, err := payloadHashForPick(productID)
	if err != nil {
		return fmt.Errorf("assembly.Repository.InsertPickOutboxEvent build hash: %w", err)
	}

	const query = `
		INSERT INTO public.outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload_hash)
		VALUES ($1, $2, 'picking', 'wms.picking.v1', $3)`

	_, err = r.q.Exec(ctx, query, eventID, productID, payloadHash)
	if err != nil {
		return fmt.Errorf("assembly.Repository.InsertPickOutboxEvent exec: %w", err)
	}
	return nil
}

// GetTasks возвращает список задач для сборщика
func (r *Repository) GetTasks(ctx context.Context, destinationID, operatorID uuid.UUID, status string) ([]TaskItem, error) {
	baseQuery := `
		SELECT
			t.event_id, t.product_id, p.qr_code, s.name as sku_name,
			b.code as from_bin_code, b.section as from_bin_section,
			o.external_order_no
		FROM wms_ops.assembly_tasks t
		JOIN wms_inventory.products p ON p.product_id = t.product_id
		JOIN wms_inventory.skus s ON s.sku_id = t.sku_id
		JOIN wms_inventory.bins b ON b.bin_id = t.from_bin_id
		JOIN wms_inventory.orders o ON o.order_id = t.order_id
		WHERE o.customer_id = $1 AND t.status = $2`

	var rows pgx.Rows
	var err error

	if operatorID != uuid.Nil {
		query := baseQuery + ` AND (t.operator_id IS NULL OR t.operator_id = $3)
		ORDER BY b.section, b.code`
		rows, err = r.q.Query(ctx, query, destinationID, status, operatorID)
	} else {
		query := baseQuery + `
		ORDER BY b.section, b.code`
		rows, err = r.q.Query(ctx, query, destinationID, status)
	}

	if err != nil {
		return nil, fmt.Errorf("assembly.Repository.GetTasks query: %w", err)
	}
	defer rows.Close()

	tasks := make([]TaskItem, 0)
	for rows.Next() {
		var t TaskItem
		var eventID uuid.UUID
		if err := rows.Scan(&eventID, &t.ProductID, &t.QRCode, &t.SKUName, &t.FromBinCode, &t.FromBinSection, &t.OrderNo); err != nil {
			return nil, fmt.Errorf("assembly.Repository.GetTasks scan: %w", err)
		}
		t.TaskID = eventID.String()
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// payloadHashForPick создает детерминированный хеш для outbox события
func payloadHashForPick(productID uuid.UUID) (string, error) {
	payload := struct {
		ProductID     uuid.UUID `json:"product_id"`
		AggregateType string    `json:"aggregate_type"`
		EventType     string    `json:"event_type"`
	}{
		ProductID:     productID,
		AggregateType: "picking",
		EventType:     "wms.picking.v1",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("assembly.payloadHashForPick marshal: %w", err)
	}

	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
