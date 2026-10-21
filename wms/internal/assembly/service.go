package assembly

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
	"wms/internal/signedops"
)

// allocateOrderBatchLimit — сколько NEW-заказов магазина Allocate лочит и обрабатывает за вызов.
// Bounded, чтобы не захватывать тысячи row-локов на больших пулах заказов (см.
// GetOrdersByDestinationForUpdate). Каденс/повторные вызовы добирают остаток FIFO.
const allocateOrderBatchLimit = 500

type Service struct {
	repo   assemblyRepository
	ops    operationStore
	signer signerSource
}

type assemblyRepository interface {
	WithTx(ctx context.Context, fn func(assemblyRepository) error) error
	GetOrdersByDestinationForUpdate(ctx context.Context, destinationID uuid.UUID, limit int) ([]domain.Order, error)
	GetOrderLinesByOrderIDs(ctx context.Context, orderIDs []uuid.UUID) (map[uuid.UUID][]domain.OrderLine, error)
	GetAllocateProductsForSKU(ctx context.Context, skuID uuid.UUID, limit int) ([]domain.Product, error)
	GetSKUByID(ctx context.Context, skuID uuid.UUID) (*domain.SKU, error)
	UpdateProductAllocated(ctx context.Context, productID, orderID uuid.UUID) error
	GetBinSectionByID(ctx context.Context, binID uuid.UUID) (string, error)
	BatchInsertAssemblyTasks(ctx context.Context, tasks []Task) error
	UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, status string) error
	GetPendingTaskByProductForUpdate(ctx context.Context, productID uuid.UUID) (*Task, error)
	GetProductByIDForUpdate(ctx context.Context, productID uuid.UUID) (*domain.Product, error)
	MarkTaskDone(ctx context.Context, eventID, operatorID uuid.UUID) error
	SetProductAssembled(ctx context.Context, productID uuid.UUID) error
	GetTasks(ctx context.Context, destinationID, operatorID uuid.UUID, status string) ([]TaskItem, error)
	GetShippingBufferBinByID(ctx context.Context, bufferBinID uuid.UUID) (*domain.Bin, error)
	ListOperatorBasket(ctx context.Context, operatorID, destinationID uuid.UUID) ([]BasketItem, error)
	LockOperatorBasket(ctx context.Context, operatorID, destinationID uuid.UUID) ([]BasketItem, error)
	MoveProductsToShippingBuffer(ctx context.Context, productIDs []uuid.UUID, bufferBinID uuid.UUID) error
	StampTaskUnload(ctx context.Context, actionID uuid.UUID, productIDs []uuid.UUID, operatorID, destinationID uuid.UUID, occurredAt time.Time) error
	UpdateOrdersToAssembled(ctx context.Context, orderIDs []uuid.UUID) (int, error)
	CountAssembledByOperator(ctx context.Context, operatorID uuid.UUID) (int, error)
	LockOperation(ctx context.Context, operationID, createdBy uuid.UUID) (*signedops.Operation, error)
	LockSigner(ctx context.Context, credentialID []byte) error
	WriteActions(ctx context.Context, op *signedops.Operation, payloads [][]byte) error
	CheckChainStatus(ctx context.Context, productIDs []uuid.UUID, aggregateType string) error
}

func NewService(repo assemblyRepository, ops operationStore, signer signerSource) *Service {
	return &Service{repo: repo, ops: ops, signer: signer}
}

type allocateCache struct {
	// short — сток по SKU, увиденный при нехватке: GetAllocateProductsForSKU вернул
	// меньше, чем нужно строке. Живёт только внутри одного вызова Allocate (одной
	// транзакции): сток, закоммиченный чужими транзакциями в середине вызова,
	// подберёт следующий вызов Allocate.
	short map[uuid.UUID]int
	// skuNames — имена SKU из GetSKUByID (кэшируются только при успешном ответе).
	skuNames map[uuid.UUID]string
	// sections — секции ячеек из GetBinSectionByID (кэшируются только при nil-ошибке).
	sections map[uuid.UUID]string
}

func newAllocateCache() *allocateCache {
	return &allocateCache{
		short:    make(map[uuid.UUID]int),
		skuNames: make(map[uuid.UUID]string),
		sections: make(map[uuid.UUID]string),
	}
}

// Allocate - выполняет аллокацию для магазина
func (s *Service) Allocate(ctx context.Context, destinationID uuid.UUID) (*AllocateResponse, error) {
	if destinationID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.Allocate: %w", ErrInvalidInput)
	}

	var resp AllocateResponse

	err := s.repo.WithTx(ctx, func(txRepo assemblyRepository) error {
		orders, err := txRepo.GetOrdersByDestinationForUpdate(ctx, destinationID, allocateOrderBatchLimit)
		if err != nil {
			return fmt.Errorf("assembly.Service.Allocate get orders: %w", err)
		}

		var orderLines map[uuid.UUID][]domain.OrderLine
		if len(orders) > 0 {
			orderIDs := make([]uuid.UUID, 0, len(orders))
			for i := range orders {
				orderIDs = append(orderIDs, orders[i].OrderID)
			}
			orderLines, err = txRepo.GetOrderLinesByOrderIDs(ctx, orderIDs)
			if err != nil {
				return fmt.Errorf("assembly.Service.Allocate get order lines: %w", err)
			}
		}

		cache := newAllocateCache()

		allocatedOrders := 0
		allocatedProducts := 0
		insufficientOrders := make([]InsufficientOrder, 0)

		for i := range orders {
			order := &orders[i]

			allocated, insufficientSKUs, err := s.allocateOrderTx(ctx, txRepo, cache, order.OrderID, destinationID, orderLines[order.OrderID])
			if err != nil {
				if errors.Is(err, ErrInsufficientStock) {
					missingSKUs := make([]InsufficientSKU, 0, len(insufficientSKUs))
					for _, isku := range insufficientSKUs {
						skuID, parseErr := uuid.Parse(isku.SKUID)
						if parseErr != nil {
							log.Printf("assembly.Service.Allocate: invalid sku_id %s: %v", isku.SKUID, parseErr)
							missingSKUs = append(missingSKUs, InsufficientSKU{
								SKUID:      isku.SKUID,
								SKUName:    "",
								MissingQty: isku.MissingQty,
							})
							continue
						}
						skuName, ok := cache.skuNames[skuID]
						if !ok {
							sku, getErr := txRepo.GetSKUByID(ctx, skuID)
							if getErr != nil {
								log.Printf("assembly.Service.Allocate: failed to get SKU name for %s: %v", skuID, getErr)
							}
							if getErr == nil && sku != nil {
								skuName = sku.Name
								cache.skuNames[skuID] = skuName
							}
						}
						missingSKUs = append(missingSKUs, InsufficientSKU{
							SKUID:      isku.SKUID,
							SKUName:    skuName,
							MissingQty: isku.MissingQty,
						})
					}
					insufficientOrders = append(insufficientOrders, InsufficientOrder{
						OrderID:     order.OrderID.String(),
						MissingSKUs: missingSKUs,
					})
					continue
				}
				return fmt.Errorf("assembly.Service.Allocate allocate order %s: %w", order.OrderID, err)
			}
			allocatedOrders++
			allocatedProducts += allocated
		}

		resp = AllocateResponse{
			AllocatedOrders:    allocatedOrders,
			AllocatedProducts:  allocatedProducts,
			InsufficientOrders: insufficientOrders,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// allocateOrderTx аллоцирует один заказ в существующей транзакции.
// lines — строки заказа, выбранные одним запросом в Allocate; заказ без записи
// в этом наборе ведёт себя как заказ без строк.
func (s *Service) allocateOrderTx(ctx context.Context, txRepo assemblyRepository, cache *allocateCache, orderID, destinationID uuid.UUID, lines []domain.OrderLine) (int, []InsufficientSKU, error) {
	allocatedProducts := make([]AllocatedProduct, 0)
	insufficientSKUs := make([]InsufficientSKU, 0)

	for _, line := range lines {
		if avail, ok := cache.short[line.SKUID]; ok && line.Qty > avail {
			insufficientSKUs = append(insufficientSKUs, InsufficientSKU{
				SKUID:      line.SKUID.String(),
				MissingQty: line.Qty - avail,
			})
			continue
		}

		products, err := txRepo.GetAllocateProductsForSKU(ctx, line.SKUID, line.Qty)
		if err != nil {
			return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx get products for SKU %s: %w", line.SKUID, err)
		}

		if len(products) < line.Qty {
			cache.short[line.SKUID] = len(products)
			insufficientSKUs = append(insufficientSKUs, InsufficientSKU{
				SKUID:      line.SKUID.String(),
				MissingQty: line.Qty - len(products),
			})
			continue
		}

		for i := range products {
			p := products[i]
			if p.BinID == nil {
				return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx product %s has no bin", p.ProductID)
			}
			allocatedProducts = append(allocatedProducts, AllocatedProduct{
				ProductID:     p.ProductID,
				OrderID:       orderID,
				BinID:         *p.BinID,
				SKUID:         line.SKUID,
				DestinationID: destinationID,
			})
		}
	}

	if len(insufficientSKUs) > 0 {
		return 0, insufficientSKUs, ErrInsufficientStock
	}

	occurredAt := time.Now().UTC()
	tasks := make([]Task, 0, len(allocatedProducts))

	for _, a := range allocatedProducts {
		if err := txRepo.UpdateProductAllocated(ctx, a.ProductID, a.OrderID); err != nil {
			return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx update product: %w", err)
		}

		section, ok := cache.sections[a.BinID]
		if !ok {
			var err error
			section, err = txRepo.GetBinSectionByID(ctx, a.BinID)
			if err != nil {
				return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx get bin section: %w", err)
			}
			cache.sections[a.BinID] = section
		}

		tasks = append(tasks, Task{
			EventID:       uuid.New(),
			OrderID:       a.OrderID,
			ProductID:     a.ProductID,
			SKUID:         a.SKUID,
			FromBinID:     a.BinID,
			Section:       section,
			DestinationID: a.DestinationID,
			OccurredAt:    occurredAt,
		})
	}

	if err := txRepo.BatchInsertAssemblyTasks(ctx, tasks); err != nil {
		return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx insert tasks: %w", err)
	}

	if err := txRepo.UpdateOrderStatus(ctx, orderID, string(domain.OrderStatusAllocated)); err != nil {
		return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx update order status: %w", err)
	}

	// Заказ аллоцирован — его строки реально израсходовали сток, который предыдущие
	// заказы этого вызова видели как нехватку.
	for _, line := range lines {
		if avail, ok := cache.short[line.SKUID]; ok {
			avail -= line.Qty
			if avail < 0 {
				avail = 0
			}
			cache.short[line.SKUID] = avail
		}
	}

	return len(allocatedProducts), nil, nil
}

// GetTasks возвращает список задач для сборщика
func (s *Service) GetTasks(ctx context.Context, destinationID, operatorID uuid.UUID, status string) (*TaskResponse, error) {
	if destinationID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.GetTasks: %w", ErrInvalidInput)
	}
	if status == "" {
		status = string(domain.TaskStatusPending)
	}

	tasks, err := s.repo.GetTasks(ctx, destinationID, operatorID, status)
	if err != nil {
		return nil, fmt.Errorf("assembly.Service.GetTasks: %w", err)
	}

	return &TaskResponse{Tasks: tasks}, nil
}

// Pick выполняет подбор товара оператором
func (s *Service) Pick(ctx context.Context, operatorID, productID uuid.UUID) (*PickResponse, error) {
	if operatorID == uuid.Nil || productID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.Pick: %w", ErrInvalidInput)
	}

	// #45: gate on the previous FSM stage. Reject the pick if the product's putaway
	// event is FAILED on-chain — the WMS must not advance a product whose prior stage
	// diverged from the ledger. Runs before the transaction (read-only pre-check).
	if err := s.repo.CheckChainStatus(ctx, []uuid.UUID{productID}, ledger.AggregatePutaway); err != nil {
		return nil, fmt.Errorf("assembly.Service.Pick: %w", err)
	}

	var task *Task

	err := s.repo.WithTx(ctx, func(txRepo assemblyRepository) error {
		var err error
		task, err = txRepo.GetPendingTaskByProductForUpdate(ctx, productID)
		if err != nil {
			return err
		}

		product, err := txRepo.GetProductByIDForUpdate(ctx, productID)
		if err != nil {
			return err
		}
		if product.Status != domain.ProductStatusAllocated {
			return ErrProductNotAllocated
		}

		if err := txRepo.MarkTaskDone(ctx, task.EventID, operatorID); err != nil {
			return fmt.Errorf("assembly.Service.Pick mark task done: %w", err)
		}

		// #14: подбор больше не пишет outbox-событие — событие Pick в цепь уходит
		// одним подписанным действием при выгрузке корзины в буфер отгрузки.
		if err := txRepo.SetProductAssembled(ctx, productID); err != nil {
			return fmt.Errorf("assembly.Service.Pick set product assembled: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// #46: cart size is DERIVED from DB (the operator's ASSEMBLED products with a DONE
	// task), not an in-memory map, so it survives a WMS restart and horizontal scaling.
	cartSize, err := s.repo.CountAssembledByOperator(ctx, operatorID)
	if err != nil {
		return nil, fmt.Errorf("assembly.Service.Pick cart size: %w", err)
	}

	return &PickResponse{
		ProductID: productID.String(),
		CartSize:  cartSize,
	}, nil
}
