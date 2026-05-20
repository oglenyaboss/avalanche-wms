package assembly

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

type Service struct {
	repo  assemblyRepository
	carts map[string][]uuid.UUID
	mu    sync.RWMutex
}

type assemblyRepository interface {
	WithTx(ctx context.Context, fn func(assemblyRepository) error) error
	GetOrdersByDestinationForUpdate(ctx context.Context, destinationID uuid.UUID) ([]domain.Order, error)
	GetOrderLinesByOrderID(ctx context.Context, orderID uuid.UUID) ([]domain.OrderLine, error)
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
	InsertPickOutboxEvent(ctx context.Context, productID, eventID uuid.UUID) error
	GetTasks(ctx context.Context, destinationID, operatorID uuid.UUID, status string) ([]TaskItem, error)
	GetShippingBufferBinByID(ctx context.Context, bufferBinID uuid.UUID) (*domain.Bin, error)
	ValidateCartDestination(ctx context.Context, productIDs []uuid.UUID, expectedDestinationID uuid.UUID) error
	UpdateProductsToReadyToShip(ctx context.Context, productIDs []uuid.UUID, binID uuid.UUID) (int, error)
	UpdateOrdersToAssembled(ctx context.Context, orderIDs []uuid.UUID) (int, error)
	GetOrderIDsByProductIDs(ctx context.Context, productIDs []uuid.UUID) ([]uuid.UUID, error)
}

func NewService(repo assemblyRepository) *Service {
	return &Service{
		repo:  repo,
		carts: make(map[string][]uuid.UUID),
	}
}

// Allocate - выполняет аллокацию для магазина
func (s *Service) Allocate(ctx context.Context, destinationID uuid.UUID) (*AllocateResponse, error) {
	if destinationID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.Allocate: %w", ErrInvalidInput)
	}

	var resp AllocateResponse

	err := s.repo.WithTx(ctx, func(txRepo assemblyRepository) error {
		orders, err := txRepo.GetOrdersByDestinationForUpdate(ctx, destinationID)
		if err != nil {
			return fmt.Errorf("assembly.Service.Allocate get orders: %w", err)
		}

		allocatedOrders := 0
		allocatedProducts := 0
		insufficientOrders := make([]InsufficientOrder, 0)

		for i := range orders {
			order := &orders[i]

			allocated, insufficientSKUs, err := s.allocateOrderTx(ctx, txRepo, order.OrderID, destinationID)
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
						sku, getErr := txRepo.GetSKUByID(ctx, skuID)
						if getErr != nil {
							log.Printf("assembly.Service.Allocate: failed to get SKU name for %s: %v", skuID, getErr)
						}
						skuName := ""
						if sku != nil {
							skuName = sku.Name
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

// allocateOrderTx аллоцирует один заказ в существующей транзакции
func (s *Service) allocateOrderTx(ctx context.Context, txRepo assemblyRepository, orderID, destinationID uuid.UUID) (int, []InsufficientSKU, error) {
	lines, err := txRepo.GetOrderLinesByOrderID(ctx, orderID)
	if err != nil {
		return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx get lines: %w", err)
	}

	allocatedProducts := make([]AllocatedProduct, 0)
	insufficientSKUs := make([]InsufficientSKU, 0)

	for _, line := range lines {
		products, err := txRepo.GetAllocateProductsForSKU(ctx, line.SKUID, line.Qty)
		if err != nil {
			return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx get products for SKU %s: %w", line.SKUID, err)
		}

		if len(products) < line.Qty {
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

		section, err := txRepo.GetBinSectionByID(ctx, a.BinID)
		if err != nil {
			return 0, nil, fmt.Errorf("assembly.Service.allocateOrderTx get bin section: %w", err)
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

		if err := txRepo.SetProductAssembled(ctx, productID); err != nil {
			return fmt.Errorf("assembly.Service.Pick set product assembled: %w", err)
		}

		if err := txRepo.InsertPickOutboxEvent(ctx, productID, task.EventID); err != nil {
			return fmt.Errorf("assembly.Service.Pick insert outbox: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	key := operatorID.String()
	s.mu.Lock()
	s.carts[key] = append(s.carts[key], productID)
	cartSize := len(s.carts[key])
	s.mu.Unlock()

	return &PickResponse{
		ProductID: productID.String(),
		CartSize:  cartSize,
	}, nil
}

// ScanShippingBuffer размещает товары из корзины оператора в буфер отгрузки магазина
func (s *Service) ScanShippingBuffer(ctx context.Context, operatorID, bufferBinID uuid.UUID) (*ScanShippingBufferResponse, error) {
	if operatorID == uuid.Nil || bufferBinID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.ScanShippingBuffer: %w", ErrInvalidInput)
	}

	key := operatorID.String()
	s.mu.RLock()
	cart := make([]uuid.UUID, len(s.carts[key]))
	copy(cart, s.carts[key])
	s.mu.RUnlock()

	if len(cart) == 0 {
		return nil, ErrCartEmpty
	}

	var bufferBin *domain.Bin
	var productsPlaced int
	var ordersAssembled int

	err := s.repo.WithTx(ctx, func(txRepo assemblyRepository) error {
		var err error
		bufferBin, err = txRepo.GetShippingBufferBinByID(ctx, bufferBinID)
		if err != nil {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer get bin: %w", err)
		}

		if bufferBin.DestinationID == nil {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer: %w", ErrDestinationNotFound)
		}

		if err := txRepo.ValidateCartDestination(ctx, cart, *bufferBin.DestinationID); err != nil {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer validate destination: %w", err)
		}

		placed, err := txRepo.UpdateProductsToReadyToShip(ctx, cart, bufferBinID)
		if err != nil {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer update products: %w", err)
		}
		if placed != len(cart) {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer placed %d of %d: %w", placed, len(cart), ErrPartialPlacement)
		}
		productsPlaced = placed

		orderIDs, err := txRepo.GetOrderIDsByProductIDs(ctx, cart)
		if err != nil {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer get order IDs: %w", err)
		}

		assembled, err := txRepo.UpdateOrdersToAssembled(ctx, orderIDs)
		if err != nil {
			return fmt.Errorf("assembly.Service.ScanShippingBuffer update orders: %w", err)
		}
		ordersAssembled = assembled

		return nil
	})

	if err != nil {
		return nil, err
	}

	// FIX: удаляем только элементы из снапшота, а не весь cart,
	// чтобы не потерять продукты, добавленные конкурентным Pick
	s.mu.Lock()
	snapshotSet := make(map[uuid.UUID]struct{}, len(cart))
	for _, id := range cart {
		snapshotSet[id] = struct{}{}
	}
	remaining := s.carts[key][:0]
	for _, id := range s.carts[key] {
		if _, inSnapshot := snapshotSet[id]; !inSnapshot {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) == 0 {
		delete(s.carts, key)
	} else {
		s.carts[key] = remaining
	}
	s.mu.Unlock()

	return &ScanShippingBufferResponse{
		BufferBinID:     bufferBin.BinID.String(),
		ProductsPlaced:  productsPlaced,
		OrdersAssembled: ordersAssembled,
	}, nil
}

// GetCartSize возвращает размер корзины оператора
func (s *Service) GetCartSize(operatorID uuid.UUID) int {
	key := operatorID.String()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.carts[key])
}

// CleanupCart очищает корзину оператора
// TODO: Планируется вынести cart в Redis или БД
func (s *Service) CleanupCart(operatorID uuid.UUID) {
	key := operatorID.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.carts, key)
}
