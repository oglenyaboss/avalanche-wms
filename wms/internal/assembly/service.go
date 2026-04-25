package assembly

import (
	"context"
	"fmt"
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
	InsertPickOutboxEvent(ctx context.Context, productID uuid.UUID) error
	GetTasks(ctx context.Context, destinationID, operatorID uuid.UUID, status string) ([]TaskItem, error)
}

func NewService(repo assemblyRepository) *Service {
	return &Service{
		repo:  repo,
		carts: make(map[string][]uuid.UUID),
	}
}

// Allocate - вполняет аллокацию для магазина
func (s *Service) Allocate(ctx context.Context, destinationID uuid.UUID) (*AllocateResponse, error) {
	if destinationID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.Allocate: %w", ErrInvalidInput)
	}

	orders, err := s.repo.GetOrdersByDestinationForUpdate(ctx, destinationID)
	if err != nil {
		return nil, fmt.Errorf("assembly.Service.Allocate get orders: %w", err)
	}

	allocatedOrders := 0
	allocatedProducts := 0
	insufficientOrders := make([]InsufficientOrder, 0)

	for _, order := range orders {
		allocated, insufficientSKUs, err := s.allocateOrder(ctx, order.OrderID)
		if err != nil {
			if err == ErrInsufficientStock {
				// Получаем названия SKU для ответа
				missingSKUs := make([]InsufficientSKU, 0, len(insufficientSKUs))
				for _, isku := range insufficientSKUs {
					sku, _ := s.repo.GetSKUByID(ctx, uuid.MustParse(isku.SKUID))
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
			return nil, fmt.Errorf("assembly.Service.Allocate allocate order %s: %w", order.OrderID, err)
		}
		allocatedOrders++
		allocatedProducts += allocated
	}

	return &AllocateResponse{
		AllocatedOrders:    allocatedOrders,
		AllocatedProducts:  allocatedProducts,
		InsufficientOrders: insufficientOrders,
	}, nil
}

// allocateOrder аллоцирует один заказ (по id заказа берет все товары, соответствующие ему, (allocatedProducts) и сообщает о нехватке (insufficientSKUs))
func (s *Service) allocateOrder(ctx context.Context, orderID uuid.UUID) (int, []InsufficientSKU, error) {
	lines, err := s.repo.GetOrderLinesByOrderID(ctx, orderID)
	if err != nil {
		return 0, nil, fmt.Errorf("assembly.Service.allocateOrder get lines: %w", err)
	}

	allocatedProducts := make([]AllocatedProduct, 0)
	insufficientSKUs := make([]InsufficientSKU, 0)

	for _, line := range lines {
		products, err := s.repo.GetAllocateProductsForSKU(ctx, line.SKUID, line.Qty)
		if err != nil {
			return 0, nil, fmt.Errorf("assembly.Service.allocateOrder get products for SKU %s: %w", line.SKUID, err)
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
			allocatedProducts = append(allocatedProducts, AllocatedProduct{
				ProductID: p.ProductID,
				OrderID:   orderID,
				BinID:     *p.BinID,
				SKUID:     line.SKUID,
			})
		}
	}

	if len(insufficientSKUs) > 0 {
		return 0, insufficientSKUs, ErrInsufficientStock
	}

	occurredAt := time.Now().UTC()
	tasks := make([]Task, 0, len(allocatedProducts))

	err = s.repo.WithTx(ctx, func(txRepo assemblyRepository) error {
		for _, a := range allocatedProducts {
			if err := txRepo.UpdateProductAllocated(ctx, a.ProductID, a.OrderID); err != nil {
				return fmt.Errorf("assembly.Service.allocateOrder update product: %w", err)
			}

			section, err := txRepo.GetBinSectionByID(ctx, a.BinID)
			if err != nil {
				return fmt.Errorf("assembly.Service.allocateOrder get bin section: %w", err)
			}

			tasks = append(tasks, Task{
				EventID:    uuid.New(),
				OrderID:    a.OrderID,
				ProductID:  a.ProductID,
				SKUID:      a.SKUID,
				FromBinID:  a.BinID,
				Section:    section,
				OccurredAt: occurredAt,
			})
		}

		if err := txRepo.BatchInsertAssemblyTasks(ctx, tasks); err != nil {
			return fmt.Errorf("assembly.Service.allocateOrder insert tasks: %w", err)
		}

		if err := txRepo.UpdateOrderStatus(ctx, orderID, "ALLOCATED"); err != nil {
			return fmt.Errorf("assembly.Service.allocateOrder update order status: %w", err)
		}

		return nil
	})

	if err != nil {
		return 0, nil, err
	}

	return len(allocatedProducts), nil, nil
}

// GetTasks возвращает список задач для сборщика
func (s *Service) GetTasks(ctx context.Context, destinationID, operatorID uuid.UUID, status string) (*TaskResponse, error) {
	if destinationID == uuid.Nil {
		return nil, fmt.Errorf("assembly.Service.GetTasks: %w", ErrInvalidInput)
	}
	if status == "" {
		status = "PENDING"
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
		if product.Status != "ALLOCATED" {
			return ErrProductNotAllocated
		}

		if err := txRepo.MarkTaskDone(ctx, task.EventID, operatorID); err != nil {
			return fmt.Errorf("assembly.Service.Pick mark task done: %w", err)
		}

		if err := txRepo.SetProductAssembled(ctx, productID); err != nil {
			return fmt.Errorf("assembly.Service.Pick set product assembled: %w", err)
		}

		if err := txRepo.InsertPickOutboxEvent(ctx, productID); err != nil {
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

// GetCartSize возвращает размер корзины оператора
func (s *Service) GetCartSize(operatorID uuid.UUID) int {
	key := operatorID.String()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.carts[key])
}
