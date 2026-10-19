package putaway

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"wms/internal/domain"
	"wms/internal/ledger"
	"wms/internal/signedops"
)

type dbTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repository struct {
	db *pgxpool.Pool
	q  dbTX
	// tx заполнен только у tx-репозитория из WithTx: tx-методы (LockOperation,
	// LockSigner, WriteActions) делегируют в signedops с транзакцией вызываемого.
	tx pgx.Tx
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db, q: db}
}

// CheckChainStatus rejects placement when a product's receiving event is FAILED
// on-chain (issue #45). Call it before the placement transaction (it runs on r.q).
func (r *Repository) CheckChainStatus(ctx context.Context, productIDs []uuid.UUID, aggregateType string) error {
	return ledger.CheckChainStatus(ctx, r.q, productIDs, aggregateType)
}

// CountReceivedInBuffer counts products currently RECEIVED in the given buffer bin.
// DB-derived replacement for the in-memory putaway cart counter (issue #46): the cart_size
// shown after a scan, reconstructed from DB state so it survives a WMS restart.
func (r *Repository) CountReceivedInBuffer(ctx context.Context, bufferBinID uuid.UUID) (int, error) {
	const query = `
		SELECT count(*)
		FROM wms_inventory.products
		WHERE bin_id = $1 AND status = 'RECEIVED'`

	var count int
	if err := r.q.QueryRow(ctx, query, bufferBinID).Scan(&count); err != nil {
		return 0, fmt.Errorf("putaway.Repository.CountReceivedInBuffer scan: %w", err)
	}
	return count, nil
}

func (r *Repository) WithTx(ctx context.Context, fn func(putawayRepository) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("putaway.Repository.WithTx begin: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(&Repository{db: r.db, q: tx, tx: tx}); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("putaway.Repository.WithTx commit: %w", err)
	}

	committed = true
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

// GetBufferBinID возвращает буферную ячейку по ID (именно буферную!)
// Сравнение через UPPER(TRIM(...)) — устойчивость к дрифту регистра/пробелов в seed-данных.
func (r *Repository) GetBufferBinByID(ctx context.Context, bufferBinID uuid.UUID) (*domain.Bin, error) {
	const query = `
		SELECT bin_id, warehouse_id, code, section, volume, created_at, updated_at
		FROM wms_inventory.bins
		WHERE bin_id = $1 AND UPPER(TRIM(section)) = 'BUFFER'`
	var bin domain.Bin
	err := r.q.QueryRow(ctx, query, bufferBinID).Scan(&bin.BinID, &bin.WarehouseID, &bin.Code, &bin.Section, &bin.Volume, &bin.CreatedAt, &bin.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("putaway.Repository.GetBufferBinByID: %w", ErrBufferBinNotFound)
		}
		return nil, fmt.Errorf("putaway.Repository.GetBufferBinByID scan: %w", err)
	}
	return &bin, nil
}

// GetStorageBinID возвращает ячейку хранения по ID (любая ячейка кроме буфера).
// Поле bins.section — произвольная метка зоны склада. Зарезервировано 2 токена: 'BUFFER' и 'SHIPPING_BUFFER',
// размещение в которые через putaway запрещено. Прочие значения рассматриваются как зоны хранения.
// Сравнение через UPPER(TRIM(...)) — устойчивость к дрифту регистра/пробелов в seed-данных.
func (r *Repository) GetStorageBinByID(ctx context.Context, storageBinID uuid.UUID) (*domain.Bin, error) {
	// volume > 0 rejects misconfigured zero-volume bins.
	// TODO(#50): full fill-level enforcement (volume vs current_fill) is deferred.
	const query = `
		SELECT bin_id, warehouse_id, code, section, volume, created_at, updated_at
		FROM wms_inventory.bins
		WHERE bin_id = $1
		  AND section IS NOT NULL
		  AND UPPER(TRIM(section)) IS DISTINCT FROM 'BUFFER'
		  AND UPPER(TRIM(section)) IS DISTINCT FROM 'SHIPPING_BUFFER'
		  AND volume > 0`
	var bin domain.Bin
	err := r.q.QueryRow(ctx, query, storageBinID).Scan(&bin.BinID, &bin.WarehouseID, &bin.Code, &bin.Section, &bin.Volume, &bin.CreatedAt, &bin.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("putaway.Repository.GetStorageBinByID: %w", ErrStorageBinNotFound)
		}
		return nil, fmt.Errorf("putaway.Repository.GetStorageBinByID scan: %w", err)
	}
	return &bin, nil
}

// ListProductsByBufferBin возвращает товары в буферной ячейке со статусом RECEIVED
func (r *Repository) ListProductsByBufferBin(ctx context.Context, bufferBinID uuid.UUID) ([]*ProductBufferItem, error) {
	const query = `
		SELECT p.product_id, p.qr_code, p.status, s.name as sku_name
		FROM wms_inventory.products p
		JOIN wms_inventory.skus s ON s.sku_id = p.sku_id
		WHERE p.bin_id = $1 AND p.status = 'RECEIVED'
		ORDER BY p.created_at`

	rows, err := r.q.Query(ctx, query, bufferBinID)
	if err != nil {
		return nil, fmt.Errorf("putaway.Repository.ListProductsByBufferBin query: %w", err)
	}
	defer rows.Close()

	products := make([]*ProductBufferItem, 0)
	for rows.Next() {
		var p ProductBufferItem
		if err := rows.Scan(&p.ProductID, &p.QRCode, &p.Status, &p.SKUName); err != nil {
			return nil, fmt.Errorf("putaway.Repository.ListProductsByBufferBin scan: %w", err)
		}
		products = append(products, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("putaway.Repository.ListProductsByBufferBin rows: %w", err)
	}

	return products, nil
}

// GetProductByID возвращает товар по ID без блокировки. Использовать для read-only сценариев
// (валидация, отображение). Для модифицирующих операций — GetProductsByIDForUpdate внутри транзакции.
func (r *Repository) GetProductByID(ctx context.Context, productID uuid.UUID) (*domain.Product, error) {
	const query = `
		SELECT product_id, sku_id, shipment_id, cargoplace_id, box_id, qr_code, bin_id, order_id, status, created_at, updated_at
		FROM wms_inventory.products
		WHERE product_id = $1`

	var p domain.Product
	err := r.q.QueryRow(ctx, query, productID).Scan(&p.ProductID, &p.SKUID, &p.ShipmentID, &p.CargoplaceID, &p.BoxID, &p.QRCode, &p.BinID, &p.OrderID, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("putaway.Repository.GetProductByID: %w", ErrProductNotFound)
		}
		return nil, fmt.Errorf("putaway.Repository.GetProductByID scan: %w", err)
	}
	return &p, nil
}

// GetProductsByIDForUpdate возвращает товар по ID и блокирует строку для предотвращения конкурентных
// изменений. Имеет смысл только внутри транзакции (WithTx) — иначе FOR UPDATE снимется сразу после
// auto-commit и lock будет фиктивным.
func (r *Repository) GetProductsByIDForUpdate(ctx context.Context, productID uuid.UUID) (*domain.Product, error) {
	const query = `
		SELECT product_id, sku_id, shipment_id, cargoplace_id, box_id, qr_code, bin_id, order_id, status, created_at, updated_at
		FROM wms_inventory.products
		WHERE product_id = $1
		FOR UPDATE`

	var p domain.Product
	err := r.q.QueryRow(ctx, query, productID).Scan(&p.ProductID, &p.SKUID, &p.ShipmentID, &p.CargoplaceID, &p.BoxID, &p.QRCode, &p.BinID, &p.OrderID, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("putaway.Repository.GetProductsByIDForUpdate: %w", ErrProductNotFound)
		}
		return nil, fmt.Errorf("putaway.Repository.GetProductsByIDForUpdate scan: %w", err)
	}
	return &p, nil
}

// GetSKUByProductID возвращает SKU товара
func (r *Repository) GetSKUByProductID(ctx context.Context, productID uuid.UUID) (*domain.SKU, error) {
	const query = `
		SELECT s.sku_id, s.name, s.description, s.volume, s.created_at, s.updated_at
		FROM wms_inventory.products p
		JOIN wms_inventory.skus s ON s.sku_id = p.sku_id
		WHERE product_id = $1`

	var sku domain.SKU
	err := r.q.QueryRow(ctx, query, productID).Scan(&sku.SKUID, &sku.Name, &sku.Description, &sku.Volume, &sku.CreatedAt, &sku.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("putaway.Repository.GetSKUByProductID: %w", ErrProductNotFound)
		}
		return nil, fmt.Errorf("putaway.Repository.GetSKUByProductID scan: %w", err)
	}
	return &sku, nil
}

// UpdateProductStorage обновляет bin_id и статус товара на STORED
func (r *Repository) UpdateProductStorage(ctx context.Context, productID, binID uuid.UUID) error {
	const query = `
		UPDATE wms_inventory.products
		SET bin_id = $2, status = 'STORED', updated_at = NOW()
		WHERE product_id = $1 AND status = 'RECEIVED'`

	tag, err := r.q.Exec(ctx, query, productID, binID)
	if err != nil {
		return fmt.Errorf("putaway.Repository.UpdateProductStorage exec: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotReceived
	}

	return nil
}

// InsertPutaway создает запись о размещении в wms_ops.putaway
func (r *Repository) InsertPutaway(ctx context.Context, params *InsertPutawayParams) error {
	const query = `
		INSERT INTO wms_ops.putaways(event_id, product_id, from_bin_id, bin_id, operator_id, occurred_at, action_id)
		VALUES($1, $2, $3, $4, $5, $6, $7)`

	_, err := r.q.Exec(ctx, query, params.EventID, params.ProductID, params.FromBinID, params.BinID, params.OperatorID, params.OccurredAt, params.ActionID)
	if err != nil {
		return fmt.Errorf("putaway.Repository.InsertPutaway exec: %w", err)
	}

	return nil
}
