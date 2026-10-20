package shipping

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedops"
)

type mockShippingRepo struct {
	bin    *BufferBinRecord
	binErr error

	products    []ReadyToShipProduct
	productsErr error

	dispatch    *DispatchRecord
	dispatchErr error

	// updatedDispatch/updateDispatchErr задают результат conditional UPDATE SCHEDULED -> AT_GATE.
	// Если updatedDispatch nil, mock имитирует отсутствие RETURNING-строки после UPDATE.
	updatedDispatch   *DispatchRecord
	updateDispatchErr error

	// updateCalls считает, сколько раз service попытался выполнить conditional UPDATE.
	updateCalls int

	// Подписанная отгрузка (ship_signed): read-only список черновика, заблокированный
	// набор submit'а, врата неподтверждённой сборки и подписанный механизм.
	listProducts        []ProductForShip
	lockedProducts      []ProductForShip
	unconfirmedProducts []uuid.UUID
	unconfirmedAsked    []uuid.UUID
	lockedOperation     *signedops.Operation
	lockSignerErr       error

	ordersCompleted        int
	ordersPartiallyShipped int

	bufferRemaining int

	withTxCalls          int
	updatedProductIDs    []uuid.UUID
	updatedOrderIDs      []uuid.UUID
	insertedShippings    []shippingInsert
	dispatchDeparted     bool
	writeActionsCalls    int
	lastWriteActionsOp   *signedops.Operation
	lastWriteActionsPayl [][]byte
}

// shippingInsert — один вызов BatchInsertShippings, зафиксированный моком.
type shippingInsert struct {
	Events     []shippingEvent
	DispatchID uuid.UUID
	OperatorID uuid.UUID
	OccurredAt time.Time
	ActionID   uuid.UUID
}

func (m *mockShippingRepo) WithTx(_ context.Context, fn func(shippingRepository) error) error {
	m.withTxCalls++
	return fn(m)
}

func (m *mockShippingRepo) GetBinWithDestinationByID(_ context.Context, _ uuid.UUID) (*BufferBinRecord, error) {
	if m.binErr != nil {
		return nil, m.binErr
	}
	return m.bin, nil
}

func (m *mockShippingRepo) GetBufferBinForUpdate(_ context.Context, _ uuid.UUID) (*BufferBinRecord, error) {
	if m.binErr != nil {
		return nil, m.binErr
	}
	return m.bin, nil
}

func (m *mockShippingRepo) ListReadyToShipProductsByBin(_ context.Context, _ uuid.UUID) ([]ReadyToShipProduct, error) {
	if m.productsErr != nil {
		return nil, m.productsErr
	}
	return m.products, nil
}

func (m *mockShippingRepo) GetDispatchByCode(_ context.Context, _ string) (*DispatchRecord, error) {
	if m.dispatchErr != nil {
		return nil, m.dispatchErr
	}
	return m.dispatch, nil
}

func (m *mockShippingRepo) GetDispatchByID(_ context.Context, _ uuid.UUID) (*DispatchRecord, error) {
	if m.dispatchErr != nil {
		return nil, m.dispatchErr
	}
	return m.dispatch, nil
}

func (m *mockShippingRepo) GetDispatchForUpdate(_ context.Context, _ uuid.UUID) (*DispatchRecord, error) {
	if m.dispatchErr != nil {
		return nil, m.dispatchErr
	}
	return m.dispatch, nil
}

// UpdateDispatchToAtGate имитирует conditional UPDATE SCHEDULED -> AT_GATE с возвратом обновлённой строки.
func (m *mockShippingRepo) UpdateDispatchToAtGate(_ context.Context, _ string) (*DispatchRecord, error) {
	m.updateCalls++
	if m.updateDispatchErr != nil {
		return nil, m.updateDispatchErr
	}
	if m.updatedDispatch == nil {
		return nil, ErrDispatchNotFound
	}
	return m.updatedDispatch, nil
}

func (m *mockShippingRepo) ListProductsForShip(_ context.Context, _ uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error) {
	return m.listProducts, nil
}

func (m *mockShippingRepo) LockProductsForShip(_ context.Context, _ uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error) {
	return m.lockedProducts, nil
}

func (m *mockShippingRepo) UnconfirmedProducts(_ context.Context, productIDs []uuid.UUID, _ string) ([]uuid.UUID, error) {
	m.unconfirmedAsked = append([]uuid.UUID(nil), productIDs...)
	return m.unconfirmedProducts, nil
}

func (m *mockShippingRepo) BatchUpdateProductsShipped(_ context.Context, productIDs []uuid.UUID) error {
	m.updatedProductIDs = append([]uuid.UUID(nil), productIDs...)
	return nil
}

func (m *mockShippingRepo) BatchInsertShippings(
	_ context.Context,
	events []shippingEvent,
	dispatchID uuid.UUID,
	operatorID uuid.UUID,
	occurredAt time.Time,
	actionID uuid.UUID,
) error {
	m.insertedShippings = append(m.insertedShippings, shippingInsert{
		Events:     append([]shippingEvent(nil), events...),
		DispatchID: dispatchID,
		OperatorID: operatorID,
		OccurredAt: occurredAt,
		ActionID:   actionID,
	})
	return nil
}

func (m *mockShippingRepo) UpdateOrdersShippedConditional(_ context.Context, orderIDs []uuid.UUID) (int, int, error) {
	m.updatedOrderIDs = append([]uuid.UUID(nil), orderIDs...)
	return m.ordersCompleted, m.ordersPartiallyShipped, nil
}

func (m *mockShippingRepo) CountReadyToShipProductsInBuffer(_ context.Context, _ uuid.UUID) (int, error) {
	return m.bufferRemaining, nil
}

func (m *mockShippingRepo) UpdateDispatchDeparted(_ context.Context, _ uuid.UUID) error {
	m.dispatchDeparted = true
	return nil
}

func (m *mockShippingRepo) LockOperation(_ context.Context, _, _ uuid.UUID) (*signedops.Operation, error) {
	if m.lockedOperation == nil {
		return nil, signedops.ErrOperationNotFound
	}
	return m.lockedOperation, nil
}

func (m *mockShippingRepo) LockSigner(_ context.Context, _ []byte) error {
	return m.lockSignerErr
}

func (m *mockShippingRepo) WriteActions(_ context.Context, op *signedops.Operation, payloads [][]byte) error {
	m.writeActionsCalls++
	m.lastWriteActionsOp = op
	m.lastWriteActionsPayl = payloads
	return nil
}

// TestScanBufferSuccess проверяет успешное сканирование буфера отгрузки и формирование ответа с товарами.
func TestScanBufferSuccess(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	destCode := "SHOP-5"
	destName := "Магазин Петровка"
	orderExternalNo := "ORD-1001"

	repo := &mockShippingRepo{
		bin: &BufferBinRecord{
			BinID:           bufferBinID,
			Code:            "BIN-SHIP-5",
			Section:         &section,
			DestinationID:   &destinationID,
			DestinationCode: &destCode,
			DestinationName: &destName,
		},
		products: []ReadyToShipProduct{
			{
				ProductID:       uuid.New(),
				QRCode:          "QR-001",
				SKUName:         "Ноутбук Lenovo X1",
				OrderExternalNo: &orderExternalNo,
			},
		},
	}

	result, err := NewService(repo, nil, nil).ScanBuffer(context.Background(), operatorID, bufferBinID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.BufferBin.ID != bufferBinID {
		t.Fatalf("expected buffer bin ID %s, got %s", bufferBinID, result.BufferBin.ID)
	}
	if result.BufferBin.Destination.ID != destinationID {
		t.Fatalf("expected destination ID %s, got %s", destinationID, result.BufferBin.Destination.ID)
	}
	if result.Count != 1 {
		t.Fatalf("expected count 1, got %d", result.Count)
	}
	if len(result.Products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(result.Products))
	}
	if result.Products[0].OrderExternalNo == nil || *result.Products[0].OrderExternalNo != orderExternalNo {
		t.Fatalf("expected order_external_no %s, got %v", orderExternalNo, result.Products[0].OrderExternalNo)
	}
}

// TestScanBufferEmpty проверяет, что пустой буфер отгрузки возвращается успешным ответом.
func TestScanBufferEmpty(t *testing.T) {
	operatorID := uuid.New()
	bufferBinID := uuid.New()
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	destCode := "SHOP-5"
	destName := "Магазин Петровка"

	repo := &mockShippingRepo{
		bin: &BufferBinRecord{
			BinID:           bufferBinID,
			Code:            "BIN-SHIP-5",
			Section:         &section,
			DestinationID:   &destinationID,
			DestinationCode: &destCode,
			DestinationName: &destName,
		},
		products: []ReadyToShipProduct{},
	}

	result, err := NewService(repo, nil, nil).ScanBuffer(context.Background(), operatorID, bufferBinID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Count != 0 {
		t.Fatalf("expected count 0, got %d", result.Count)
	}
	if len(result.Products) != 0 {
		t.Fatalf("expected empty products, got %d", len(result.Products))
	}
}

// TestScanBufferBinNotFound проверяет ошибку, когда буферная ячейка не найдена.
func TestScanBufferBinNotFound(t *testing.T) {
	repo := &mockShippingRepo{binErr: ErrBinNotFound}

	_, err := NewService(repo, nil, nil).ScanBuffer(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrBinNotFound) {
		t.Fatalf("expected ErrBinNotFound, got %v", err)
	}
}

// TestScanBufferWrongSection проверяет ошибку, когда найденная ячейка не является SHIPPING_BUFFER.
func TestScanBufferWrongSection(t *testing.T) {
	section := "A"
	repo := &mockShippingRepo{
		bin: &BufferBinRecord{
			BinID:   uuid.New(),
			Code:    "A-01",
			Section: &section,
		},
	}

	_, err := NewService(repo, nil, nil).ScanBuffer(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrBinNotShippingBuffer) {
		t.Fatalf("expected ErrBinNotShippingBuffer, got %v", err)
	}
}

// TestScanDriverSuccess проверяет успешный переход рейса из SCHEDULED в AT_GATE.
func TestScanDriverSuccess(t *testing.T) {
	operatorID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()
	arrivedAt := time.Now().UTC()

	repo := &mockShippingRepo{
		dispatch: &DispatchRecord{
			DispatchID:   dispatchID,
			DispatchCode: "DSP-001",
			Status:       domain.OutboundDispatchStatusScheduled,
		},
		updatedDispatch: &DispatchRecord{
			DispatchID:      dispatchID,
			DispatchCode:    "DSP-001",
			DestinationID:   destinationID,
			DestinationCode: "SHOP-5",
			DestinationName: "Магазин Петровка",
			VehicleNumber:   "A123BC777",
			DriverName:      "Иван Петров",
			Status:          domain.OutboundDispatchStatusAtGate,
			ArrivedAt:       &arrivedAt,
		},
	}

	result, err := NewService(repo, nil, nil).ScanDriver(context.Background(), operatorID, "DSP-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.updateCalls != 1 {
		t.Fatalf("expected 1 update call, got %d", repo.updateCalls)
	}
	if result.Status != domain.OutboundDispatchStatusAtGate {
		t.Fatalf("expected status AT_GATE, got %s", result.Status)
	}
	if result.ArrivedAt == nil {
		t.Fatal("expected arrived_at to be set")
	}
}

// TestScanDriverNotFound проверяет ошибку, когда рейс не найден ни через UPDATE, ни через SELECT.
func TestScanDriverNotFound(t *testing.T) {
	repo := &mockShippingRepo{dispatchErr: ErrDispatchNotFound}

	_, err := NewService(repo, nil, nil).ScanDriver(context.Background(), uuid.New(), "DSP-MISSING")
	if !errors.Is(err, ErrDispatchNotFound) {
		t.Fatalf("expected ErrDispatchNotFound, got %v", err)
	}
}

// TestScanDriverAlreadyDeparted проверяет запрет повторного сканирования уже уехавшего рейса.
func TestScanDriverAlreadyDeparted(t *testing.T) {
	repo := &mockShippingRepo{
		dispatch: &DispatchRecord{
			DispatchID:   uuid.New(),
			DispatchCode: "DSP-001",
			Status:       domain.OutboundDispatchStatusDeparted,
		},
	}

	_, err := NewService(repo, nil, nil).ScanDriver(context.Background(), uuid.New(), "DSP-001")
	if !errors.Is(err, ErrDispatchAlreadyDeparted) {
		t.Fatalf("expected ErrDispatchAlreadyDeparted, got %v", err)
	}
	if repo.updateCalls != 1 {
		t.Fatalf("expected 1 update call, got %d", repo.updateCalls)
	}
}

// TestScanDriverCancelled проверяет ошибку при сканировании отменённого рейса.
func TestScanDriverCancelled(t *testing.T) {
	repo := &mockShippingRepo{
		dispatch: &DispatchRecord{
			DispatchID:   uuid.New(),
			DispatchCode: "DSP-001",
			Status:       domain.OutboundDispatchStatusCancelled,
		},
	}

	_, err := NewService(repo, nil, nil).ScanDriver(context.Background(), uuid.New(), "DSP-001")
	if !errors.Is(err, ErrDispatchCancelled) {
		t.Fatalf("expected ErrDispatchCancelled, got %v", err)
	}
	if repo.updateCalls != 1 {
		t.Fatalf("expected 1 update call, got %d", repo.updateCalls)
	}
}

// TestScanDriverIdempotentAtGate проверяет идемпотентный повторный скан рейса, который уже находится AT_GATE.
func TestScanDriverIdempotentAtGate(t *testing.T) {
	operatorID := uuid.New()
	dispatchID := uuid.New()
	destinationID := uuid.New()
	arrivedAt := time.Now().UTC()

	repo := &mockShippingRepo{
		dispatch: &DispatchRecord{
			DispatchID:      dispatchID,
			DispatchCode:    "DSP-001",
			DestinationID:   destinationID,
			DestinationCode: "SHOP-5",
			DestinationName: "Магазин Петровка",
			VehicleNumber:   "A123BC777",
			DriverName:      "Иван Петров",
			Status:          domain.OutboundDispatchStatusAtGate,
			ArrivedAt:       &arrivedAt,
		},
	}

	result, err := NewService(repo, nil, nil).ScanDriver(context.Background(), operatorID, "DSP-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.updateCalls != 1 {
		t.Fatalf("expected 1 update call, got %d", repo.updateCalls)
	}
	if result.DispatchID != dispatchID {
		t.Fatalf("expected dispatch ID %s, got %s", dispatchID, result.DispatchID)
	}
	if result.Status != domain.OutboundDispatchStatusAtGate {
		t.Fatalf("expected status AT_GATE, got %s", result.Status)
	}
	if result.ArrivedAt == nil || !result.ArrivedAt.Equal(arrivedAt) {
		t.Fatalf("expected arrived_at %s, got %v", arrivedAt, result.ArrivedAt)
	}
}
