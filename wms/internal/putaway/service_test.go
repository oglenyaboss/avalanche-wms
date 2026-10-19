package putaway

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/ledger"
	"wms/internal/signedaction"
	"wms/internal/signedops"
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

	// GetProductsByIDForUpdate - используем map для разных productID
	productsMap     map[uuid.UUID]*domain.Product
	productErr      error
	getProductCount int

	// GetSKUByProductID
	sku    *domain.SKU
	skuErr error

	// UpdateProductStorage
	updateProductErr  error
	updatedProductIDs []uuid.UUID
	updatedBinIDs     []uuid.UUID
	updateCallCount   int
	failOnUpdateCall  int

	// InsertPutaway
	insertPutawayErr error
	insertedPutaways []*InsertPutawayParams

	// LockOperation/LockSigner/WriteActions — tx-методы подписанного submit
	lockedOperation          *signedops.Operation
	lockOperationErr         error
	lockSignerErr            error
	writeActionsErr          error
	writeActionsCalls        int
	lastWriteActionsOp       *signedops.Operation
	lastWriteActionsPayloads [][]byte

	// WithTx tracking
	withTxCalls int
	inTx        bool

	// CheckChainStatus (#45 chain-status gate)
	checkChainErr error
	checkChainAgg string // captures the aggregateType the service passed to the gate

	// CountReceivedInBuffer (#46 DB-derived cart size)
	receivedInBuffer int
}

func (m *mockPutawayRepo) WithTx(_ context.Context, fn func(putawayRepository) error) error {
	m.withTxCalls++
	m.inTx = true
	defer func() { m.inTx = false }()
	return fn(m)
}

func (m *mockPutawayRepo) CheckChainStatus(_ context.Context, _ []uuid.UUID, aggregateType string) error {
	m.checkChainAgg = aggregateType
	return m.checkChainErr
}

func (m *mockPutawayRepo) CountReceivedInBuffer(_ context.Context, _ uuid.UUID) (int, error) {
	return m.receivedInBuffer, nil
}

// TestDraftPlaceToStorageBin_GateRejected verifies the #45 gate: when a
// product's receiving event is FAILED on-chain, the draft is rejected with
// ErrChainEventRejected — no operation is stored, no transaction opens.
func TestDraftPlaceToStorageBin_GateRejected(t *testing.T) {
	bufferBinID := uuid.New()
	productID := uuid.New()
	mockRepo := &mockPutawayRepo{
		checkChainErr: ledger.ErrChainEventRejected,
		productsMap: map[uuid.UUID]*domain.Product{
			productID: {ProductID: productID, Status: "RECEIVED", BinID: &bufferBinID},
		},
	}
	svc := newTestService(mockRepo)

	_, _, err := svc.DraftPlaceToStorageBin(context.Background(), uuid.New(), []uuid.UUID{productID}, uuid.New())
	if !errors.Is(err, ledger.ErrChainEventRejected) {
		t.Fatalf("expected ErrChainEventRejected, got %v", err)
	}
	if mockRepo.withTxCalls != 0 {
		t.Fatalf("gate must reject before the transaction; withTxCalls=%d", mockRepo.withTxCalls)
	}
	if mockRepo.checkChainAgg != ledger.AggregateReceiving {
		t.Fatalf("DraftPlaceToStorageBin must gate on the receiving stage; CheckChainStatus aggregateType=%q, want %q", mockRepo.checkChainAgg, ledger.AggregateReceiving)
	}
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

func (m *mockPutawayRepo) GetProductByID(_ context.Context, productID uuid.UUID) (*domain.Product, error) {
	m.getProductCount++
	if m.productErr != nil {
		return nil, m.productErr
	}
	if m.productsMap != nil {
		if product, ok := m.productsMap[productID]; ok {
			productCopy := *product
			return &productCopy, nil
		}
	}
	return nil, ErrProductNotFound
}

func (m *mockPutawayRepo) GetProductsByIDForUpdate(_ context.Context, productID uuid.UUID) (*domain.Product, error) {
	m.getProductCount++
	if m.productErr != nil {
		return nil, m.productErr
	}
	if m.productsMap != nil {
		if product, ok := m.productsMap[productID]; ok {
			productCopy := *product
			return &productCopy, nil
		}
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

func (m *mockPutawayRepo) LockOperation(_ context.Context, _, _ uuid.UUID) (*signedops.Operation, error) {
	if m.lockOperationErr != nil {
		return nil, m.lockOperationErr
	}
	return m.lockedOperation, nil
}

func (m *mockPutawayRepo) LockSigner(_ context.Context, _ []byte) error {
	return m.lockSignerErr
}

func (m *mockPutawayRepo) WriteActions(_ context.Context, op *signedops.Operation, payloads [][]byte) error {
	m.writeActionsCalls++
	m.lastWriteActionsOp = op
	m.lastWriteActionsPayloads = payloads
	return m.writeActionsErr
}

// newTestService собирает Service с пустыми signed-моками для сценариев, где
// операция не участвует (buffer scan, cart scan).
func newTestService(repo *mockPutawayRepo) *Service {
	return NewService(repo, &mockOperationStore{operations: map[uuid.UUID]*signedops.Operation{}}, &mockSignerSource{})
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

	svc := newTestService(mockRepo)
	result, err := svc.GetBufferProducts(context.Background(), operatorID, bufferBinID)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.BufferBinID != bufferBinID.String() {
		t.Fatalf("expected BufferBinID %s, got %s", bufferBinID.String(), result.BufferBinID)
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

	svc := newTestService(mockRepo)
	_, err := svc.GetBufferProducts(context.Background(), operatorID, bufferBinID)

	if !errors.Is(err, ErrBufferBinNotFound) {
		t.Fatalf("expected ErrBufferBinNotFound, got %v", err)
	}
}

// TestGetBufferProductsInvalidInput - невалидные входные данные
func TestGetBufferProductsInvalidInput(t *testing.T) {
	svc := newTestService(&mockPutawayRepo{})

	_, err := svc.GetBufferProducts(context.Background(), uuid.Nil, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil operatorID, got %v", err)
	}

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
		productsMap: map[uuid.UUID]*domain.Product{
			productID: {
				ProductID: productID,
				QRCode:    "QR-001",
				Status:    "RECEIVED",
				BinID:     &binID,
			},
		},
		sku: &domain.SKU{
			SKUID: uuid.New(),
			Name:  "Ноутбук Lenovo X1",
		},
		// Derived cart size: one RECEIVED product staged in this buffer bin (#46).
		receivedInBuffer: 1,
	}

	svc := newTestService(mockRepo)

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
}

// TestAddToPutawayCartDuplicateProduct - проверка дедупликации товаров в корзине
func TestAddToPutawayCartDuplicateProduct(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	bufferBinID := uuid.New()
	binID := bufferBinID

	mockRepo := &mockPutawayRepo{
		productsMap: map[uuid.UUID]*domain.Product{
			productID: {
				ProductID: productID,
				QRCode:    "QR-001",
				Status:    "RECEIVED",
				BinID:     &binID,
			},
		},
		sku: &domain.SKU{
			SKUID: uuid.New(),
			Name:  "Ноутбук Lenovo X1",
		},
		// Re-scanning the same product does not grow the count: the derived size is the
		// number of RECEIVED products in the bin, unchanged by a duplicate scan (#46).
		receivedInBuffer: 1,
	}

	svc := newTestService(mockRepo)

	result1, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result1.CartSize != 1 {
		t.Fatalf("expected CartSize 1, got %d", result1.CartSize)
	}

	result2, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result2.CartSize != 1 {
		t.Fatalf("expected CartSize still 1, got %d", result2.CartSize)
	}
}

// TestAddToPutawayCartProductNotInBuffer - товар не в указанном буфере
func TestAddToPutawayCartProductNotInBuffer(t *testing.T) {
	operatorID := uuid.New()
	productID := uuid.New()
	bufferBinID := uuid.New()
	wrongBinID := uuid.New()

	mockRepo := &mockPutawayRepo{
		productsMap: map[uuid.UUID]*domain.Product{
			productID: {
				ProductID: productID,
				QRCode:    "QR-001",
				Status:    "RECEIVED",
				BinID:     &wrongBinID,
			},
		},
	}

	svc := newTestService(mockRepo)
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
		productsMap: map[uuid.UUID]*domain.Product{
			productID: {
				ProductID: productID,
				QRCode:    "QR-001",
				Status:    "STORED",
				BinID:     &binID,
			},
		},
	}

	svc := newTestService(mockRepo)
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

	svc := newTestService(mockRepo)
	_, err := svc.AddToPutawayCart(context.Background(), operatorID, productID, bufferBinID)

	if !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

// TestAddToPutawayCartInvalidInput - невалидные входные данные
func TestAddToPutawayCartInvalidInput(t *testing.T) {
	svc := newTestService(&mockPutawayRepo{})

	_, err := svc.AddToPutawayCart(context.Background(), uuid.Nil, uuid.New(), uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil operatorID, got %v", err)
	}

	_, err = svc.AddToPutawayCart(context.Background(), uuid.New(), uuid.Nil, uuid.New())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil productID, got %v", err)
	}

	_, err = svc.AddToPutawayCart(context.Background(), uuid.New(), uuid.New(), uuid.Nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for nil bufferBinID, got %v", err)
	}
}

// TestServiceImplementsInterface - проверка что Service реализует интерфейс
func TestServiceImplementsInterface(_ *testing.T) {
	var _ interface {
		GetBufferProducts(context.Context, uuid.UUID, uuid.UUID) (*ScanBufferResponse, error)
		AddToPutawayCart(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*ScanProductResponse, error)
		DraftPlaceToStorageBin(context.Context, uuid.UUID, []uuid.UUID, uuid.UUID) (*signedops.Operation, string, error)
		SubmitPlaceToStorageBin(context.Context, uuid.UUID, uuid.UUID, map[uuid.UUID]signedaction.Assertion) (*ScanStorageBinResponse, error)
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
	svc := newTestService(mockRepo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.GetBufferProducts(context.Background(), operatorID, bufferBinID)
	}
}
