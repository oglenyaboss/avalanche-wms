package assembly

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"wms/internal/domain"
)

// mockAssemblyRepo - мок-репозиторий для тестирования сервиса
type mockAssemblyRepo struct {
	// GetOrdersByDestinationForUpdate
	orders    []domain.Order
	ordersErr error

	// GetOrderLinesByOrderID
	orderLines    map[uuid.UUID][]domain.OrderLine
	orderLinesErr error

	// GetAllocateProductsForSKU
	productsMap map[uuid.UUID][]domain.Product
	productsErr error

	// GetSKUByID
	skusMap map[uuid.UUID]*domain.SKU
	skusErr error

	// UpdateProductAllocated
	updateProductErr    error
	updatedProducts     []uuid.UUID
	updatedProductsLock sync.Mutex

	// GetBinSectionByID
	binSections    map[uuid.UUID]string
	binSectionsErr error

	// BatchInsertAssemblyTasks
	batchInsertErr error
	insertedTasks  []Task

	// UpdateOrderStatus
	updateOrderStatusErr error
	updatedOrderStatus   map[uuid.UUID]string

	// GetPendingTaskByProductForUpdate
	pendingTasks    map[uuid.UUID]*Task
	pendingTasksErr error

	// GetProductByIDForUpdate
	productsByID    map[uuid.UUID]*domain.Product
	productsByIDErr error

	// MarkTaskDone
	markTaskDoneErr error
	markedTasks     []uuid.UUID

	// SetProductAssembled
	setProductAssembledErr error
	assembledProducts      []uuid.UUID

	// InsertPickOutboxEvent
	insertOutboxErr error
	outboxCalls     int

	// GetTasks
	tasksResult []TaskItem
	tasksErr    error

	// AreAllTasksDoneForOrder
	areAllTasksDone    bool
	areAllTasksDoneErr error

	// WithTx tracking
	withTxMu    sync.Mutex
	withTxCalls int
	withTxFn    func(func(assemblyRepository) error) error
}

func (m *mockAssemblyRepo) WithTx(_ context.Context, fn func(assemblyRepository) error) error {
	m.withTxMu.Lock()
	m.withTxCalls++
	m.withTxMu.Unlock()
	if m.withTxFn != nil {
		return m.withTxFn(fn)
	}
	return fn(m)
}

func (m *mockAssemblyRepo) GetOrdersByDestinationForUpdate(_ context.Context, _ uuid.UUID) ([]domain.Order, error) {
	if m.ordersErr != nil {
		return nil, m.ordersErr
	}
	return m.orders, nil
}

func (m *mockAssemblyRepo) GetOrderLinesByOrderID(_ context.Context, orderID uuid.UUID) ([]domain.OrderLine, error) {
	if m.orderLinesErr != nil {
		return nil, m.orderLinesErr
	}
	if lines, ok := m.orderLines[orderID]; ok {
		return lines, nil
	}
	return []domain.OrderLine{}, nil
}

func (m *mockAssemblyRepo) GetAllocateProductsForSKU(_ context.Context, skuID uuid.UUID, limit int) ([]domain.Product, error) {
	if m.productsErr != nil {
		return nil, m.productsErr
	}
	if products, ok := m.productsMap[skuID]; ok {
		if len(products) > limit {
			return products[:limit], nil
		}
		return products, nil
	}
	return []domain.Product{}, nil
}

func (m *mockAssemblyRepo) GetSKUByID(_ context.Context, skuID uuid.UUID) (*domain.SKU, error) {
	if m.skusErr != nil {
		return nil, m.skusErr
	}
	if sku, ok := m.skusMap[skuID]; ok {
		return sku, nil
	}
	return nil, nil
}

func (m *mockAssemblyRepo) UpdateProductAllocated(_ context.Context, productID, _ uuid.UUID) error {
	m.updatedProductsLock.Lock()
	defer m.updatedProductsLock.Unlock()
	m.updatedProducts = append(m.updatedProducts, productID)
	if m.updateProductErr != nil {
		return m.updateProductErr
	}
	return nil
}

func (m *mockAssemblyRepo) GetBinSectionByID(_ context.Context, binID uuid.UUID) (string, error) {
	if m.binSectionsErr != nil {
		return "", m.binSectionsErr
	}
	if section, ok := m.binSections[binID]; ok {
		return section, nil
	}
	return "", nil
}

func (m *mockAssemblyRepo) BatchInsertAssemblyTasks(_ context.Context, tasks []Task) error {
	m.insertedTasks = append(m.insertedTasks, tasks...)
	if m.batchInsertErr != nil {
		return m.batchInsertErr
	}
	return nil
}

func (m *mockAssemblyRepo) UpdateOrderStatus(_ context.Context, orderID uuid.UUID, status string) error {
	if m.updatedOrderStatus == nil {
		m.updatedOrderStatus = make(map[uuid.UUID]string)
	}
	m.updatedOrderStatus[orderID] = status
	if m.updateOrderStatusErr != nil {
		return m.updateOrderStatusErr
	}
	return nil
}

func (m *mockAssemblyRepo) GetPendingTaskByProductForUpdate(_ context.Context, productID uuid.UUID) (*Task, error) {
	if m.pendingTasksErr != nil {
		return nil, m.pendingTasksErr
	}
	if task, ok := m.pendingTasks[productID]; ok {
		return task, nil
	}
	return nil, ErrNoTaskForProduct
}

func (m *mockAssemblyRepo) GetProductByIDForUpdate(_ context.Context, productID uuid.UUID) (*domain.Product, error) {
	if m.productsByIDErr != nil {
		return nil, m.productsByIDErr
	}
	if product, ok := m.productsByID[productID]; ok {
		return product, nil
	}
	return nil, ErrProductNotAllocated
}

func (m *mockAssemblyRepo) MarkTaskDone(_ context.Context, eventID, _ uuid.UUID) error {
	m.markedTasks = append(m.markedTasks, eventID)
	if m.markTaskDoneErr != nil {
		return m.markTaskDoneErr
	}
	return nil
}

func (m *mockAssemblyRepo) SetProductAssembled(_ context.Context, productID uuid.UUID) error {
	m.assembledProducts = append(m.assembledProducts, productID)
	if m.setProductAssembledErr != nil {
		return m.setProductAssembledErr
	}
	return nil
}

func (m *mockAssemblyRepo) InsertPickOutboxEvent(_ context.Context, _ uuid.UUID) error {
	m.outboxCalls++
	if m.insertOutboxErr != nil {
		return m.insertOutboxErr
	}
	return nil
}

func (m *mockAssemblyRepo) GetTasks(_ context.Context, _, _ uuid.UUID, _ string) ([]TaskItem, error) {
	if m.tasksErr != nil {
		return nil, m.tasksErr
	}
	return m.tasksResult, nil
}

func (m *mockAssemblyRepo) AreAllTasksDoneForOrder(_ context.Context, _ uuid.UUID) (bool, error) {
	if m.areAllTasksDoneErr != nil {
		return false, m.areAllTasksDoneErr
	}
	return m.areAllTasksDone, nil
}

func (m *mockAssemblyRepo) UpdateOrderStatusToAssembled(_ context.Context, orderID uuid.UUID) error {
	if m.updateOrderStatusErr != nil {
		return m.updateOrderStatusErr
	}
	if m.updatedOrderStatus == nil {
		m.updatedOrderStatus = make(map[uuid.UUID]string)
	}
	m.updatedOrderStatus[orderID] = string(domain.OrderStatusAssembled)
	return nil
}

// TestAllocateHappyPath - успешная аллокация одного заказа
func TestAllocateHappyPath(t *testing.T) {
	destinationID := uuid.New()
	orderID := uuid.New()
	skuID := uuid.New()
	productID1 := uuid.New()
	productID2 := uuid.New()
	binID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		orders: []domain.Order{
			{OrderID: orderID, ExternalOrderNo: "ORD-001", Status: "NEW"},
		},
		orderLines: map[uuid.UUID][]domain.OrderLine{
			orderID: {
				{ID: 1, OrderID: orderID, SKUID: skuID, Qty: 2},
			},
		},
		productsMap: map[uuid.UUID][]domain.Product{
			skuID: {
				{ProductID: productID1, BinID: &binID},
				{ProductID: productID2, BinID: &binID},
			},
		},
		binSections: map[uuid.UUID]string{
			binID: "A",
		},
		skusMap: map[uuid.UUID]*domain.SKU{
			skuID: {SKUID: skuID, Name: "Test SKU"},
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.Allocate(context.Background(), destinationID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.AllocatedOrders != 1 {
		t.Fatalf("expected AllocatedOrders 1, got %d", result.AllocatedOrders)
	}
	if result.AllocatedProducts != 2 {
		t.Fatalf("expected AllocatedProducts 2, got %d", result.AllocatedProducts)
	}
	if len(result.InsufficientOrders) != 0 {
		t.Fatalf("expected no insufficient orders, got %d", len(result.InsufficientOrders))
	}
	if len(mockRepo.updatedProducts) != 2 {
		t.Fatalf("expected 2 products updated, got %d", len(mockRepo.updatedProducts))
	}
	if len(mockRepo.insertedTasks) != 2 {
		t.Fatalf("expected 2 tasks inserted, got %d", len(mockRepo.insertedTasks))
	}
	if status, ok := mockRepo.updatedOrderStatus[orderID]; !ok || status != "ALLOCATED" {
		t.Fatalf("expected order status ALLOCATED, got %s", status)
	}
}

// TestAllocateInsufficientStock - недостаточно товаров для заказа
func TestAllocateInsufficientStock(t *testing.T) {
	destinationID := uuid.New()
	orderID := uuid.New()
	skuID := uuid.New()
	skuID2 := uuid.New()
	productID1 := uuid.New()

	mockRepo := &mockAssemblyRepo{
		orders: []domain.Order{
			{OrderID: orderID, ExternalOrderNo: "ORD-001", Status: "NEW"},
		},
		orderLines: map[uuid.UUID][]domain.OrderLine{
			orderID: {
				{ID: 1, OrderID: orderID, SKUID: skuID, Qty: 3},
				{ID: 2, OrderID: orderID, SKUID: skuID2, Qty: 1},
			},
		},
		productsMap: map[uuid.UUID][]domain.Product{
			skuID:  {{ProductID: productID1, BinID: nil}},
			skuID2: {},
		},
		skusMap: map[uuid.UUID]*domain.SKU{
			skuID:  {SKUID: skuID, Name: "SKU A"},
			skuID2: {SKUID: skuID2, Name: "SKU B"},
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.Allocate(context.Background(), destinationID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.AllocatedOrders != 0 {
		t.Fatalf("expected AllocatedOrders 0, got %d", result.AllocatedOrders)
	}
	if len(result.InsufficientOrders) != 1 {
		t.Fatalf("expected 1 insufficient order, got %d", len(result.InsufficientOrders))
	}
	if len(mockRepo.updatedProducts) != 0 {
		t.Fatalf("expected no products updated, got %d", len(mockRepo.updatedProducts))
	}
}

// TestAllocateMultipleOrders - частичная аллокация (один заказ успешен, другой нет)
func TestAllocateMultipleOrders(t *testing.T) {
	destinationID := uuid.New()
	orderID1 := uuid.New()
	orderID2 := uuid.New()
	skuID := uuid.New()
	productID1 := uuid.New()
	productID2 := uuid.New()
	binID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		orders: []domain.Order{
			{OrderID: orderID1, ExternalOrderNo: "ORD-001", Status: "NEW"},
			{OrderID: orderID2, ExternalOrderNo: "ORD-002", Status: "NEW"},
		},
		orderLines: map[uuid.UUID][]domain.OrderLine{
			orderID1: {{ID: 1, OrderID: orderID1, SKUID: skuID, Qty: 1}},
			orderID2: {{ID: 2, OrderID: orderID2, SKUID: skuID, Qty: 5}},
		},
		productsMap: map[uuid.UUID][]domain.Product{
			skuID: {
				{ProductID: productID1, BinID: &binID},
				{ProductID: productID2, BinID: &binID},
			},
		},
		binSections: map[uuid.UUID]string{
			binID: "A",
		},
		skusMap: map[uuid.UUID]*domain.SKU{
			skuID: {SKUID: skuID, Name: "Test SKU"},
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.Allocate(context.Background(), destinationID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.AllocatedOrders != 1 {
		t.Fatalf("expected AllocatedOrders 1, got %d", result.AllocatedOrders)
	}
	if len(result.InsufficientOrders) != 1 {
		t.Fatalf("expected 1 insufficient order, got %d", len(result.InsufficientOrders))
	}
}

// TestPickHappyPath - успешный подбор товара
func TestPickHappyPath(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	orderID := uuid.New()
	eventID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		pendingTasks: map[uuid.UUID]*Task{
			productID: {EventID: eventID, ProductID: productID, OrderID: orderID, Status: "PENDING"},
		},
		productsByID: map[uuid.UUID]*domain.Product{
			productID: {ProductID: productID, Status: "ALLOCATED"},
		},
		areAllTasksDone: false, // не все задачи выполнены
	}

	svc := NewService(mockRepo)
	result, err := svc.Pick(context.Background(), operatorID, productID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ProductID != productID.String() {
		t.Fatalf("expected ProductID %s, got %s", productID.String(), result.ProductID)
	}
	if result.CartSize != 1 {
		t.Fatalf("expected CartSize 1, got %d", result.CartSize)
	}
	if len(mockRepo.markedTasks) != 1 {
		t.Fatalf("expected task marked done, got %d", len(mockRepo.markedTasks))
	}
	if len(mockRepo.assembledProducts) != 1 {
		t.Fatalf("expected product assembled, got %d", len(mockRepo.assembledProducts))
	}
	if mockRepo.outboxCalls != 1 {
		t.Fatalf("expected outbox event, got %d", mockRepo.outboxCalls)
	}
	if mockRepo.withTxCalls != 1 {
		t.Fatalf("expected WithTx called, got %d", mockRepo.withTxCalls)
	}
}

// TestPickUpdatesOrderStatusWhenAllTasksDone - проверка обновления статуса заказа
func TestPickUpdatesOrderStatusWhenAllTasksDone(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	orderID := uuid.New()
	eventID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		pendingTasks: map[uuid.UUID]*Task{
			productID: {EventID: eventID, ProductID: productID, OrderID: orderID, Status: "PENDING"},
		},
		productsByID: map[uuid.UUID]*domain.Product{
			productID: {ProductID: productID, Status: "ALLOCATED"},
		},
		areAllTasksDone: true, // все задачи выполнены
	}

	svc := NewService(mockRepo)
	result, err := svc.Pick(context.Background(), operatorID, productID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ProductID != productID.String() {
		t.Fatalf("expected ProductID %s, got %s", productID.String(), result.ProductID)
	}
	if result.CartSize != 1 {
		t.Fatalf("expected CartSize 1, got %d", result.CartSize)
	}

	if status, ok := mockRepo.updatedOrderStatus[orderID]; !ok {
		t.Fatalf("expected order status to be updated")
	} else if status != string(domain.OrderStatusAssembled) {
		t.Fatalf("expected order status ASSEMBLED, got %s", status)
	}
}

// TestPickNoTask - попытка подобрать товар без задачи
func TestPickNoTask(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		pendingTasks:    map[uuid.UUID]*Task{},
		pendingTasksErr: ErrNoTaskForProduct,
	}

	svc := NewService(mockRepo)
	_, err := svc.Pick(context.Background(), operatorID, productID)

	if !errors.Is(err, ErrNoTaskForProduct) {
		t.Fatalf("expected ErrNoTaskForProduct, got %v", err)
	}
}

// TestPickProductNotAllocated - товар не в статусе ALLOCATED
func TestPickProductNotAllocated(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	orderID := uuid.New()
	eventID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		pendingTasks: map[uuid.UUID]*Task{
			productID: {EventID: eventID, ProductID: productID, OrderID: orderID, Status: "PENDING"},
		},
		productsByID: map[uuid.UUID]*domain.Product{
			productID: {ProductID: productID, Status: "STORED"},
		},
	}

	svc := NewService(mockRepo)
	_, err := svc.Pick(context.Background(), operatorID, productID)

	if !errors.Is(err, ErrProductNotAllocated) {
		t.Fatalf("expected ErrProductNotAllocated, got %v", err)
	}
}

// TestPickRaceCondition - гонка: два оператора пытаются подобрать один товар
func TestPickRaceCondition(t *testing.T) {
	operatorID1 := uuid.New()
	operatorID2 := uuid.New()
	productID := uuid.New()
	orderID := uuid.New()
	eventID := uuid.New()

	callCount := 0
	var mu sync.Mutex

	mockRepo := &mockAssemblyRepo{
		pendingTasks: map[uuid.UUID]*Task{
			productID: {EventID: eventID, ProductID: productID, OrderID: orderID, Status: "PENDING"},
		},
		productsByID: map[uuid.UUID]*domain.Product{
			productID: {ProductID: productID, Status: "ALLOCATED"},
		},
	}

	mockRepo.withTxFn = func(fn func(assemblyRepository) error) error {
		mu.Lock()
		callCount++
		currentCall := callCount
		mu.Unlock()

		if currentCall == 1 {
			return fn(mockRepo)
		}
		return ErrNoTaskForProduct
	}

	svc := NewService(mockRepo)

	var wg sync.WaitGroup
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err1 = svc.Pick(context.Background(), operatorID1, productID)
	}()
	go func() {
		defer wg.Done()
		_, err2 = svc.Pick(context.Background(), operatorID2, productID)
	}()

	wg.Wait()

	if err1 == nil && err2 == nil {
		t.Fatalf("expected one of the picks to fail, both succeeded")
	}
	if err1 != nil && !errors.Is(err1, ErrNoTaskForProduct) && !errors.Is(err1, ErrProductNotAllocated) {
		t.Fatalf("unexpected error for first operator: %v", err1)
	}
	if err2 != nil && !errors.Is(err2, ErrNoTaskForProduct) && !errors.Is(err2, ErrProductNotAllocated) {
		t.Fatalf("unexpected error for second operator: %v", err2)
	}
}

// TestPickCartSize - проверка увеличения корзины при последовательных pick'ах
func TestPickCartSize(t *testing.T) {
	operatorID := uuid.New()
	orderID := uuid.New()
	productID1 := uuid.New()
	productID2 := uuid.New()
	productID3 := uuid.New()
	eventID1 := uuid.New()
	eventID2 := uuid.New()
	eventID3 := uuid.New()

	mockRepo := &mockAssemblyRepo{
		pendingTasks: map[uuid.UUID]*Task{
			productID1: {EventID: eventID1, ProductID: productID1, OrderID: orderID, Status: "PENDING"},
			productID2: {EventID: eventID2, ProductID: productID2, OrderID: orderID, Status: "PENDING"},
			productID3: {EventID: eventID3, ProductID: productID3, OrderID: orderID, Status: "PENDING"},
		},
		productsByID: map[uuid.UUID]*domain.Product{
			productID1: {ProductID: productID1, Status: "ALLOCATED"},
			productID2: {ProductID: productID2, Status: "ALLOCATED"},
			productID3: {ProductID: productID3, Status: "ALLOCATED"},
		},
	}

	svc := NewService(mockRepo)

	result1, _ := svc.Pick(context.Background(), operatorID, productID1)
	if result1.CartSize != 1 {
		t.Fatalf("expected CartSize 1, got %d", result1.CartSize)
	}

	result2, _ := svc.Pick(context.Background(), operatorID, productID2)
	if result2.CartSize != 2 {
		t.Fatalf("expected CartSize 2, got %d", result2.CartSize)
	}

	result3, _ := svc.Pick(context.Background(), operatorID, productID3)
	if result3.CartSize != 3 {
		t.Fatalf("expected CartSize 3, got %d", result3.CartSize)
	}

	if size := svc.GetCartSize(operatorID); size != 3 {
		t.Fatalf("expected final cart size 3, got %d", size)
	}
}

// TestGetTasksSuccess - успешное получение списка задач
func TestGetTasksSuccess(t *testing.T) {
	destinationID := uuid.New()
	operatorID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		tasksResult: []TaskItem{
			{TaskID: uuid.New().String(), ProductID: uuid.New().String(), QRCode: "QR-001", SKUName: "SKU A", FromBinCode: "BIN-01", FromBinSection: "A", OrderNo: "ORD-001"},
			{TaskID: uuid.New().String(), ProductID: uuid.New().String(), QRCode: "QR-002", SKUName: "SKU B", FromBinCode: "BIN-02", FromBinSection: "B", OrderNo: "ORD-001"},
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.GetTasks(context.Background(), destinationID, operatorID, "PENDING")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}
}

// TestGetTasksInvalidInput - невалидный destinationID
func TestGetTasksInvalidInput(t *testing.T) {
	svc := NewService(&mockAssemblyRepo{})
	_, err := svc.GetTasks(context.Background(), uuid.Nil, uuid.New(), "PENDING")

	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

// TestAllocateInvalidInput - невалидный destinationID
func TestAllocateInvalidInput(t *testing.T) {
	svc := NewService(&mockAssemblyRepo{})
	_, err := svc.Allocate(context.Background(), uuid.Nil)

	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

// TestPickInvalidInput - невалидные входные данные
func TestPickInvalidInput(t *testing.T) {
	svc := NewService(&mockAssemblyRepo{})

	_, err := svc.Pick(context.Background(), uuid.Nil, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil operatorID, got %v", err)
	}

	_, err = svc.Pick(context.Background(), uuid.New(), uuid.Nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil productID, got %v", err)
	}
}

// TestServiceImplementsInterface - проверка что Service реализует интерфейс
func TestServiceImplementsInterface(_ *testing.T) {
	var _ interface {
		Allocate(context.Context, uuid.UUID) (*AllocateResponse, error)
		GetTasks(context.Context, uuid.UUID, uuid.UUID, string) (*TaskResponse, error)
		Pick(context.Context, uuid.UUID, uuid.UUID) (*PickResponse, error)
	} = (*Service)(nil)
}

// BenchmarkAllocate - бенчмарк для аллокации
func BenchmarkAllocate(b *testing.B) {
	destinationID := uuid.New()
	orderID := uuid.New()
	skuID := uuid.New()
	productID := uuid.New()
	binID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		orders: []domain.Order{
			{OrderID: orderID, ExternalOrderNo: "ORD-001", Status: "NEW"},
		},
		orderLines: map[uuid.UUID][]domain.OrderLine{
			orderID: {{ID: 1, OrderID: orderID, SKUID: skuID, Qty: 1}},
		},
		productsMap: map[uuid.UUID][]domain.Product{
			skuID: {{ProductID: productID, BinID: &binID}},
		},
		binSections: map[uuid.UUID]string{
			binID: "A",
		},
		skusMap: map[uuid.UUID]*domain.SKU{
			skuID: {SKUID: skuID, Name: "Test SKU"},
		},
	}

	svc := NewService(mockRepo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.Allocate(context.Background(), destinationID)
	}
}

// BenchmarkPick - бенчмарк для pick
func BenchmarkPick(b *testing.B) {
	operatorID := uuid.New()
	productID := uuid.New()
	orderID := uuid.New()
	eventID := uuid.New()

	mockRepo := &mockAssemblyRepo{
		pendingTasks: map[uuid.UUID]*Task{
			productID: {EventID: eventID, ProductID: productID, OrderID: orderID, Status: "PENDING"},
		},
		productsByID: map[uuid.UUID]*domain.Product{
			productID: {ProductID: productID, Status: "ALLOCATED"},
		},
	}

	svc := NewService(mockRepo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.Pick(context.Background(), operatorID, productID)
	}
}
