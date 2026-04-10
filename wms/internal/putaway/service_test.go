package putaway

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"wms/internal/domain"
)

type mockPutawayRepo struct {
	// GetBufferBinByID
	bufferBin    *domain.Bin
	bufferBinErr error

	// GetStorageBinByID
	storageBin    *domain.Bin
	storageBinErr error

	// ListProductsByBufferBin
	products        []*ProductBufferItem
	listProductsErr error

	// GetProductsByIDForUpdate
	product    *domain.Product
	productErr error

	// GetSKUByProductID
	sku    *domain.SKU
	skuErr error

	// UpdateProductStorage
	updateProductErr  error
	updatedProductIDs []uuid.UUID
	updatedBinIDs     []uuid.UUID
	updateCallCount   int
	failOnUpdateCall  int // на каком по счету вызове вернуть ошибку

	// InsertPutaway
	insertPutawayErr error
	insertedPutaways []*InsertPutawayParams

	// InsertOutboxEvents
	insertOutboxErr error
	outboxCalls     int

	// WithTx tracking
	withTxCalls int
	inTx        bool
}

func (m *mockPutawayRepo) WithTx(_ context.Context, fn func(putawayRepository) error) error {
	m.withTxCalls++
	m.inTx = true
	defer func() { m.inTx = false }()
	return fn(m)
}

func (m *mockPutawayRepo) GetBufferBinByID(_ context.Context, _ uuid.UUID) (*domain.Bin, error) {
	if m.bufferBinErr != nil {
		return nil, m.bufferBinErr
	}
	return m.bufferBin, nil
}

func (m *mockPutawayRepo) GetStorageBinByID(_ context.Context, _ uuid.UUID) (*domain.Bin, error) {
	if m.storageBinErr != nil {
		return nil, m.storageBinErr
	}
	return m.storageBin, nil
}

func (m *mockPutawayRepo) ListProductsByBufferBin(_ context.Context, _ uuid.UUID) ([]*ProductBufferItem, error) {
	if m.listProductsErr != nil {
		return nil, m.listProductsErr
	}
	return m.products, nil
}

func (m *mockPutawayRepo) GetProductsByIDForUpdate(_ context.Context, productID uuid.UUID) (*domain.Product, error) {
	if m.productErr != nil {
		return nil, m.productErr
	}
	if m.product != nil {
		productCopy := *m.product
		productCopy.ProductID = productID
		return &productCopy, nil
	}
	return nil, ErrProductNotFound
}

func (m *mockPutawayRepo) GetSKUByProductID(_ context.Context, _ uuid.UUID) (*domain.SKU, error) {
	if m.skuErr != nil {
		return nil, m.skuErr
	}
	return m.sku, nil
}

func (m *mockPutawayRepo) UpdateProductStorage(_ context.Context, productID, binID uuid.UUID) error {
	m.updateCallCount++
	m.updatedProductIDs = append(m.updatedProductIDs, productID)
	m.updatedBinIDs = append(m.updatedBinIDs, binID)

	if m.failOnUpdateCall > 0 && m.updateCallCount == m.failOnUpdateCall {
		return errors.New("database error")
	}

	if m.updateProductErr != nil {
		return m.updateProductErr
	}
	return nil
}

func (m *mockPutawayRepo) InsertPutaway(_ context.Context, params *InsertPutawayParams) error {
	m.insertedPutaways = append(m.insertedPutaways, params)
	if m.insertPutawayErr != nil {
		return m.insertPutawayErr
	}
	return nil
}

func (m *mockPutawayRepo) InsertOutboxEvents(_ context.Context, _ *OutboxEventsParams) error {
	m.outboxCalls++
	if m.insertOutboxErr != nil {
		return m.insertOutboxErr
	}
	return nil
}

// TestGetBufferProductsSuccess - успешное получение товаров из буфера
func TestGetBufferProductsSuccess(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()

	mockRepo := &mockPutawayRepo{
		bufferBin: &domain.Bin{
			BinID: bufferBinID,
			Code:  "T1-BUF",
		},
		products: []*ProductBufferItem{
			{
				ProductID: uuid.New(),
				QRCode:    "QR-001",
				Status:    "RECEIVED",
				SKUName:   "Ноутбук Lenovo X1",
			},
			{
				ProductID: uuid.New(),
				QRCode:    "QR-002",
				Status:    "RECEIVED",
				SKUName:   "Мышь Logitech",
			},
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.GetBufferProducts(context.Background(), operatorID, bufferBinID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.BufferBinId != bufferBinID.String() {
		t.Fatalf("expected BufferBinId %s, got %s", bufferBinID.String(), result.BufferBinId)
	}
	if result.BufferCode != "T1-BUF" {
		t.Fatalf("expected BufferCode T1-BUF, got %s", result.BufferCode)
	}
	if result.TotalProducts != 2 {
		t.Fatalf("expected TotalProducts 2, got %d", result.TotalProducts)
	}
	if len(result.Products) != 2 {
		t.Fatalf("expected 2 products, got %d", len(result.Products))
	}
}

// TestGetBufferProductsBufferNotFound - буферная ячейка не найдена
func TestGetBufferProductsBufferNotFound(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()

	mockRepo := &mockPutawayRepo{
		bufferBinErr: ErrBufferBinNotFound,
	}

	svc := NewService(mockRepo)
	_, err := svc.GetBufferProducts(context.Background(), operatorID, bufferBinID)

	if !errors.Is(err, ErrBufferBinNotFound) {
		t.Fatalf("expected ErrBufferBinNotFound, got %v", err)
	}
}

// TestGetBufferProductsInvalidInput - невалидные входные данные
func TestGetBufferProductsInvalidInput(t *testing.T) {
	svc := NewService(&mockPutawayRepo{})

	// Nil operatorID
	_, err := svc.GetBufferProducts(context.Background(), uuid.Nil, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil operatorID, got %v", err)
	}

	// Nil bufferBinID
	_, err = svc.GetBufferProducts(context.Background(), uuid.New(), uuid.Nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil bufferBinID, got %v", err)
	}
}

// TestAddToPutawayCartSuccess - успешное добавление товара в корзину
func TestAddToPutawayCartSuccess(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	bufferBinID := uuid.New()
	binID := bufferBinID

	mockRepo := &mockPutawayRepo{
		product: &domain.Product{
			ProductID: productID,
			QRCode:    "QR-001",
			Status:    "RECEIVED",
			BinID:     &binID,
		},
		sku: &domain.SKU{
			SKUID: uuid.New(),
			Name:  "Ноутбук Lenovo X1",
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ProductID != productID.String() {
		t.Fatalf("expected ProductID %s, got %s", productID.String(), result.ProductID)
	}
	if result.SKUName != "Ноутбук Lenovo X1" {
		t.Fatalf("expected SKUName 'Ноутбук Lenovo X1', got %s", result.SKUName)
	}
	if result.CartSize != 1 {
		t.Fatalf("expected CartSize 1, got %d", result.CartSize)
	}
	if mockRepo.withTxCalls != 1 {
		t.Fatalf("expected WithTx to be called, got %d calls", mockRepo.withTxCalls)
	}
}

// TestAddToPutawayCartProductNotInBuffer - товар не в указанном буфере
func TestAddToPutawayCartProductNotInBuffer(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	bufferBinID := uuid.New()
	wrongBinID := uuid.New()

	mockRepo := &mockPutawayRepo{
		product: &domain.Product{
			ProductID: productID,
			QRCode:    "QR-001",
			Status:    "RECEIVED",
			BinID:     &wrongBinID,
		},
	}

	svc := NewService(mockRepo)
	_, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)

	if !errors.Is(err, ErrProductNotInBuffer) {
		t.Fatalf("expected ErrProductNotInBuffer, got %v", err)
	}
}

// TestAddToPutawayCartProductNotReceived - товар не в статусе RECEIVED
func TestAddToPutawayCartProductNotReceived(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	bufferBinID := uuid.New()
	binID := bufferBinID

	mockRepo := &mockPutawayRepo{
		product: &domain.Product{
			ProductID: productID,
			QRCode:    "QR-001",
			Status:    "STORED",
			BinID:     &binID,
		},
	}

	svc := NewService(mockRepo)
	_, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)

	if !errors.Is(err, ErrProductNotReceived) {
		t.Fatalf("expected ErrProductNotReceived, got %v", err)
	}
}

// TestAddToPutawayCartProductNotFound - товар не найден
func TestAddToPutawayCartProductNotFound(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	bufferBinID := uuid.New()

	mockRepo := &mockPutawayRepo{
		productErr: ErrProductNotFound,
	}

	svc := NewService(mockRepo)
	_, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)

	if !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

// TestAddToPutawayCartInvalidInput - невалидные входные данные
func TestAddToPutawayCartInvalidInput(t *testing.T) {
	svc := NewService(&mockPutawayRepo{})

	// Nil operatorID
	_, err := svc.AddToPutawayCart(context.Background(), uuid.Nil, uuid.New(), uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil operatorID, got %v", err)
	}

	// Nil productID
	_, err = svc.AddToPutawayCart(context.Background(), uuid.New(), uuid.Nil, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil productID, got %v", err)
	}

	// Nil bufferBinID
	_, err = svc.AddToPutawayCart(context.Background(), uuid.New(), uuid.New(), uuid.Nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil bufferBinID, got %v", err)
	}
}

// TestPlaceProductsToStorageBinSuccess - успешное размещение товаров
func TestPlaceProductsToStorageBinSuccess(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	productID1 := uuid.New()
	productID2 := uuid.New()
	productIDs := []uuid.UUID{productID1, productID2}
	binID := storageBinID

	mockRepo := &mockPutawayRepo{
		storageBin: &domain.Bin{
			BinID: storageBinID,
			Code:  "M2-A-03",
		},
		product: &domain.Product{
			ProductID: productID1,
			Status:    "RECEIVED",
			BinID:     &binID,
		},
	}

	svc := NewService(mockRepo)
	result, err := svc.PlaceProductsToStorageBin(context.Background(), operatorID, productIDs, storageBinID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.StorageBinID != storageBinID.String() {
		t.Fatalf("expected StorageBinID %s, got %s", storageBinID.String(), result.StorageBinID)
	}
	if result.StorageBinCode != "M2-A-03" {
		t.Fatalf("expected StorageBinCode M2-A-03, got %s", result.StorageBinCode)
	}
	if result.ProductsPlaced != 2 {
		t.Fatalf("expected ProductsPlaced 2, got %d", result.ProductsPlaced)
	}
	if result.OutboxEventsCreated != 2 {
		t.Fatalf("expected OutboxEventsCreated 2, got %d", result.OutboxEventsCreated)
	}
	if mockRepo.withTxCalls != 1 {
		t.Fatalf("expected WithTx to be called, got %d calls", mockRepo.withTxCalls)
	}
	if len(mockRepo.updatedProductIDs) != 2 {
		t.Fatalf("expected 2 products updated, got %d", len(mockRepo.updatedProductIDs))
	}
	if len(mockRepo.insertedPutaways) != 2 {
		t.Fatalf("expected 2 putaway records, got %d", len(mockRepo.insertedPutaways))
	}
	if mockRepo.outboxCalls != 1 {
		t.Fatalf("expected 1 outbox call, got %d", mockRepo.outboxCalls)
	}
}

// TestPlaceProductsToStorageBinStorageNotFound - ячейка хранения не найдена
func TestPlaceProductsToStorageBinStorageNotFound(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	productIDs := []uuid.UUID{uuid.New()}

	mockRepo := &mockPutawayRepo{
		storageBinErr: ErrStorageBinNotFound,
	}

	svc := NewService(mockRepo)
	_, err := svc.PlaceProductsToStorageBin(context.Background(), operatorID, productIDs, storageBinID)

	if !errors.Is(err, ErrStorageBinNotFound) {
		t.Fatalf("expected ErrStorageBinNotFound, got %v", err)
	}
}

// TestPlaceProductsToStorageBinProductNotReceived - товар не в статусе RECEIVED
func TestPlaceProductsToStorageBinProductNotReceived(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	productID := uuid.New()
	productIDs := []uuid.UUID{productID}
	binID := storageBinID

	mockRepo := &mockPutawayRepo{
		storageBin: &domain.Bin{
			BinID: storageBinID,
			Code:  "M2-A-03",
		},
		product: &domain.Product{
			ProductID: productID,
			Status:    "STORED",
			BinID:     &binID,
		},
		updateProductErr: ErrProductNotReceived,
	}

	svc := NewService(mockRepo)
	_, err := svc.PlaceProductsToStorageBin(context.Background(), operatorID, productIDs, storageBinID)

	if !errors.Is(err, ErrProductNotReceived) {
		t.Fatalf("expected ErrProductNotReceived, got %v", err)
	}
}

// TestPlaceProductsToStorageBinInvalidInput - невалидные входные данные
func TestPlaceProductsToStorageBinInvalidInput(t *testing.T) {
	svc := NewService(&mockPutawayRepo{})

	// Nil operatorID
	_, err := svc.PlaceProductsToStorageBin(context.Background(), uuid.Nil, []uuid.UUID{uuid.New()}, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil operatorID, got %v", err)
	}

	// Empty productIDs
	_, err = svc.PlaceProductsToStorageBin(context.Background(), uuid.New(), []uuid.UUID{}, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty productIDs, got %v", err)
	}

	// Nil storageBinID
	_, err = svc.PlaceProductsToStorageBin(context.Background(), uuid.New(), []uuid.UUID{uuid.New()}, uuid.Nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil storageBinID, got %v", err)
	}
}

// TestPlaceProductsToStorageBinMultipleProductsTransaction - проверка транзакционности
func TestPlaceProductsToStorageBinMultipleProductsTransaction(t *testing.T) {
	operatorID := uuid.New()
	storageBinID := uuid.New()
	productID1 := uuid.New()
	productID2 := uuid.New()
	productIDs := []uuid.UUID{productID1, productID2}
	binID := storageBinID

	mockRepo := &mockPutawayRepo{
		storageBin: &domain.Bin{
			BinID: storageBinID,
			Code:  "M2-A-03",
		},
		product: &domain.Product{
			ProductID: productID1,
			Status:    "RECEIVED",
			BinID:     &binID,
		},
		failOnUpdateCall: 2, // на втором вызове вернуть ошибку
	}

	svc := NewService(mockRepo)
	_, err := svc.PlaceProductsToStorageBin(context.Background(), operatorID, productIDs, storageBinID)

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if len(mockRepo.updatedProductIDs) != 2 {
		t.Fatalf("expected 2 update attempts, got %d", len(mockRepo.updatedProductIDs))
	}

	if mockRepo.outboxCalls > 0 {
		t.Fatalf("expected outbox not to be called on error, got %d calls", mockRepo.outboxCalls)
	}
}

// TestServiceImplementsInterface - проверка что Service реализует интерфейс
func TestServiceImplementsInterface(_ *testing.T) {
	var _ interface {
		GetBufferProducts(context.Context, uuid.UUID, uuid.UUID) (*ScanBufferResponse, error)
		AddToPutawayCart(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*ScanProductResponse, error)
		PlaceProductsToStorageBin(context.Context, uuid.UUID, []uuid.UUID, uuid.UUID) (*ScanStorageBinResponse, error)
	} = (*Service)(nil)
}

// BenchmarkGetBufferProducts - бенчмарк для GetBufferProducts
func BenchmarkGetBufferProducts(b *testing.B) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	mockRepo := &mockPutawayRepo{
		bufferBin: &domain.Bin{
			BinID: bufferBinID,
			Code:  "T1-BUF",
		},
		products: []*ProductBufferItem{
			{ProductID: uuid.New(), QRCode: "QR-001", Status: "RECEIVED", SKUName: "Product 1"},
			{ProductID: uuid.New(), QRCode: "QR-002", Status: "RECEIVED", SKUName: "Product 2"},
			{ProductID: uuid.New(), QRCode: "QR-003", Status: "RECEIVED", SKUName: "Product 3"},
		},
	}
	svc := NewService(mockRepo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.GetBufferProducts(context.Background(), operatorID, bufferBinID)
	}
}
