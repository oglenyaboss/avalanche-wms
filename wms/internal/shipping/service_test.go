package shipping

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

type mockShippingRepo struct {
	bin    *bufferBinRecord
	binErr error

	products    []readyToShipProduct
	productsErr error

	dispatch    *dispatchRecord
	dispatchErr error

	// updatedDispatch/updateDispatchErr задают результат conditional UPDATE SCHEDULED -> AT_GATE.
	// Если updatedDispatch nil, mock имитирует отсутствие RETURNING-строки после UPDATE.
	updatedDispatch   *dispatchRecord
	updateDispatchErr error

	// updateCalls считает, сколько раз service попытался выполнить conditional UPDATE.
	updateCalls int

	shipProducts    []productForShip
	shipProductsErr error

	ordersCompleted int
	updateOrdersErr error

	bufferRemaining int
	countErr        error

	updateProductsErr error
	insertShippingErr error
	insertOutboxErr   error
	departErr         error

	withTxCalls          int
	selectedSpotProducts []uuid.UUID
	updatedProductIDs    []uuid.UUID
	updatedOrderIDs      []uuid.UUID
	insertedShippings    []shippingEvent
	insertedOutbox       []shippingEvent
	dispatchDeparted     bool
}

func (m *mockShippingRepo) WithTx(_ context.Context, fn func(shippingRepository) error) error {
	m.withTxCalls++
	return fn(m)
}

func (m *mockShippingRepo) GetBinWithDestinationByID(_ context.Context, _ uuid.UUID) (*bufferBinRecord, error) {
	if m.binErr != nil {
		return nil, m.binErr
	}
	return m.bin, nil
}

func (m *mockShippingRepo) GetBufferBinForUpdate(_ context.Context, _ uuid.UUID) (*bufferBinRecord, error) {
	if m.binErr != nil {
		return nil, m.binErr
	}
	return m.bin, nil
}

func (m *mockShippingRepo) ListReadyToShipProductsByBin(_ context.Context, _ uuid.UUID) ([]readyToShipProduct, error) {
	if m.productsErr != nil {
		return nil, m.productsErr
	}
	return m.products, nil
}

func (m *mockShippingRepo) GetDispatchByCode(_ context.Context, _ string) (*dispatchRecord, error) {
	if m.dispatchErr != nil {
		return nil, m.dispatchErr
	}
	return m.dispatch, nil
}

func (m *mockShippingRepo) GetDispatchForUpdate(_ context.Context, _ uuid.UUID) (*dispatchRecord, error) {
	if m.dispatchErr != nil {
		return nil, m.dispatchErr
	}
	return m.dispatch, nil
}

// UpdateDispatchToAtGate имитирует conditional UPDATE SCHEDULED -> AT_GATE с возвратом обновлённой строки.
func (m *mockShippingRepo) UpdateDispatchToAtGate(_ context.Context, _ string) (*dispatchRecord, error) {
	m.updateCalls++
	if m.updateDispatchErr != nil {
		return nil, m.updateDispatchErr
	}
	if m.updatedDispatch == nil {
		return nil, ErrDispatchNotFound
	}
	return m.updatedDispatch, nil
}

func (m *mockShippingRepo) SelectProductsForShip(
	_ context.Context,
	_ uuid.UUID,
	productIDs []uuid.UUID,
) ([]productForShip, error) {
	m.selectedSpotProducts = append([]uuid.UUID(nil), productIDs...)
	if m.shipProductsErr != nil {
		return nil, m.shipProductsErr
	}
	return m.shipProducts, nil
}

func (m *mockShippingRepo) BatchUpdateProductsShipped(_ context.Context, productIDs []uuid.UUID) error {
	m.updatedProductIDs = append([]uuid.UUID(nil), productIDs...)
	return m.updateProductsErr
}

func (m *mockShippingRepo) BatchInsertShippings(
	_ context.Context,
	events []shippingEvent,
	_ uuid.UUID,
	_ uuid.UUID,
) error {
	m.insertedShippings = append([]shippingEvent(nil), events...)
	return m.insertShippingErr
}

func (m *mockShippingRepo) BatchInsertShippingOutbox(
	_ context.Context,
	events []shippingEvent,
	_ uuid.UUID,
) (int, error) {
	m.insertedOutbox = append([]shippingEvent(nil), events...)
	if m.insertOutboxErr != nil {
		return 0, m.insertOutboxErr
	}
	return len(events), nil
}

func (m *mockShippingRepo) UpdateOrdersShippedConditional(_ context.Context, orderIDs []uuid.UUID) (int, error) {
	m.updatedOrderIDs = append([]uuid.UUID(nil), orderIDs...)
	if m.updateOrdersErr != nil {
		return 0, m.updateOrdersErr
	}
	return m.ordersCompleted, nil
}

func (m *mockShippingRepo) CountReadyToShipProductsInBuffer(_ context.Context, _ uuid.UUID) (int, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return m.bufferRemaining, nil
}

func (m *mockShippingRepo) UpdateDispatchDeparted(_ context.Context, _ uuid.UUID) error {
	if m.departErr != nil {
		return m.departErr
	}
	m.dispatchDeparted = true
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
		bin: &bufferBinRecord{
			BinID:           bufferBinID,
			Code:            "BIN-SHIP-5",
			Section:         &section,
			DestinationID:   &destinationID,
			DestinationCode: &destCode,
			DestinationName: &destName,
		},
		products: []readyToShipProduct{
			{
				ProductID:       uuid.New(),
				QRCode:          "QR-001",
				SKUName:         "Ноутбук Lenovo X1",
				OrderExternalNo: &orderExternalNo,
			},
		},
	}

	result, err := NewService(repo).ScanBuffer(context.Background(), operatorID, bufferBinID)
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
		bin: &bufferBinRecord{
			BinID:           bufferBinID,
			Code:            "BIN-SHIP-5",
			Section:         &section,
			DestinationID:   &destinationID,
			DestinationCode: &destCode,
			DestinationName: &destName,
		},
		products: []readyToShipProduct{},
	}

	result, err := NewService(repo).ScanBuffer(context.Background(), operatorID, bufferBinID)
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

	_, err := NewService(repo).ScanBuffer(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrBinNotFound) {
		t.Fatalf("expected ErrBinNotFound, got %v", err)
	}
}

// TestScanBufferWrongSection проверяет ошибку, когда найденная ячейка не является SHIPPING_BUFFER.
func TestScanBufferWrongSection(t *testing.T) {
	section := "A"
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			BinID:   uuid.New(),
			Code:    "A-01",
			Section: &section,
		},
	}

	_, err := NewService(repo).ScanBuffer(context.Background(), uuid.New(), uuid.New())
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
		dispatch: &dispatchRecord{
			DispatchID:   dispatchID,
			DispatchCode: "DSP-001",
			Status:       domain.OutboundDispatchStatusScheduled,
		},
		updatedDispatch: &dispatchRecord{
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

	result, err := NewService(repo).ScanDriver(context.Background(), operatorID, "DSP-001")
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

	_, err := NewService(repo).ScanDriver(context.Background(), uuid.New(), "DSP-MISSING")
	if !errors.Is(err, ErrDispatchNotFound) {
		t.Fatalf("expected ErrDispatchNotFound, got %v", err)
	}
}

// TestScanDriverAlreadyDeparted проверяет запрет повторного сканирования уже уехавшего рейса.
func TestScanDriverAlreadyDeparted(t *testing.T) {
	repo := &mockShippingRepo{
		dispatch: &dispatchRecord{
			DispatchID:   uuid.New(),
			DispatchCode: "DSP-001",
			Status:       domain.OutboundDispatchStatusDeparted,
		},
	}

	_, err := NewService(repo).ScanDriver(context.Background(), uuid.New(), "DSP-001")
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
		dispatch: &dispatchRecord{
			DispatchID:   uuid.New(),
			DispatchCode: "DSP-001",
			Status:       domain.OutboundDispatchStatusCancelled,
		},
	}

	_, err := NewService(repo).ScanDriver(context.Background(), uuid.New(), "DSP-001")
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
		dispatch: &dispatchRecord{
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

	result, err := NewService(repo).ScanDriver(context.Background(), operatorID, "DSP-001")
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

// TestShipBulkSuccess проверяет bulk-отгрузку всего буфера и переход рейса в DEPARTED.
func TestShipBulkSuccess(t *testing.T) {
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	operatorID := uuid.New()
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	orderID := uuid.New()
	product1 := uuid.New()
	product2 := uuid.New()

	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			BinID:         bufferBinID,
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DispatchID:    dispatchID,
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts: []productForShip{
			{ProductID: product1, OrderID: &orderID},
			{ProductID: product2, OrderID: &orderID},
		},
		ordersCompleted: 1,
		bufferRemaining: 0,
	}

	result, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: bufferBinID,
		DispatchID:  dispatchID,
		OperatorID:  operatorID,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ProductsShipped != 2 {
		t.Fatalf("expected 2 products shipped, got %d", result.ProductsShipped)
	}
	if result.OutboxEventsCreated != 2 {
		t.Fatalf("expected 2 outbox events, got %d", result.OutboxEventsCreated)
	}
	if result.OrdersCompleted != 1 {
		t.Fatalf("expected 1 completed order, got %d", result.OrdersCompleted)
	}
	if !result.DispatchDeparted || !repo.dispatchDeparted {
		t.Fatal("expected dispatch to depart")
	}
	if len(repo.selectedSpotProducts) != 0 {
		t.Fatalf("expected bulk select with no product ids, got %d", len(repo.selectedSpotProducts))
	}
	assertSameUUIDs(t, repo.updatedProductIDs, []uuid.UUID{product1, product2})
	assertShippingEventsMatch(t, repo.insertedShippings, repo.insertedOutbox, []uuid.UUID{product1, product2})
}

// TestShipSpotSuccess проверяет spot-отгрузку выбранных товаров без отправления рейса, если в буфере есть остаток.
func TestShipSpotSuccess(t *testing.T) {
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	operatorID := uuid.New()
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	productID := uuid.New()

	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			BinID:         bufferBinID,
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DispatchID:    dispatchID,
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts:    []productForShip{{ProductID: productID}},
		bufferRemaining: 3,
	}

	result, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: bufferBinID,
		DispatchID:  dispatchID,
		OperatorID:  operatorID,
		ProductIDs:  []uuid.UUID{productID},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ProductsShipped != 1 {
		t.Fatalf("expected 1 product shipped, got %d", result.ProductsShipped)
	}
	if result.OutboxEventsCreated != 1 {
		t.Fatalf("expected 1 outbox event, got %d", result.OutboxEventsCreated)
	}
	if result.DispatchDeparted {
		t.Fatal("expected dispatch to stay at gate")
	}
	assertSameUUIDs(t, repo.selectedSpotProducts, []uuid.UUID{productID})
	assertShippingEventsMatch(t, repo.insertedShippings, repo.insertedOutbox, []uuid.UUID{productID})
}

// TestShipDestinationMismatch проверяет запрет отгрузки, когда destination буфера и рейса не совпадают.
func TestShipDestinationMismatch(t *testing.T) {
	section := binSectionShippingBuffer
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			Section:       &section,
			DestinationID: uuidPtr(uuid.New()),
		},
		dispatch: &dispatchRecord{
			DestinationID: uuid.New(),
			Status:        domain.OutboundDispatchStatusAtGate,
		},
	}

	_, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: uuid.New(),
		DispatchID:  uuid.New(),
		OperatorID:  uuid.New(),
	})
	if !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("expected ErrDestinationMismatch, got %v", err)
	}
	if len(repo.updatedProductIDs) != 0 {
		t.Fatal("expected no product updates")
	}
}

// TestShipDispatchNotAtGate проверяет запрет отгрузки для рейса, который ещё не находится у ворот.
func TestShipDispatchNotAtGate(t *testing.T) {
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusScheduled,
		},
	}

	_, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: uuid.New(),
		DispatchID:  uuid.New(),
		OperatorID:  uuid.New(),
	})
	if !errors.Is(err, ErrDispatchNotAtGate) {
		t.Fatalf("expected ErrDispatchNotAtGate, got %v", err)
	}
}

// TestShipBulkBufferEmpty проверяет ошибку bulk-отгрузки из пустого буфера.
func TestShipBulkBufferEmpty(t *testing.T) {
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts: []productForShip{},
	}

	_, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: uuid.New(),
		DispatchID:  uuid.New(),
		OperatorID:  uuid.New(),
	})
	if !errors.Is(err, ErrBufferEmpty) {
		t.Fatalf("expected ErrBufferEmpty, got %v", err)
	}
}

// TestShipSpotProductNotInBuffer проверяет ошибку spot-отгрузки, если хотя бы один товар не готов в этом буфере.
func TestShipSpotProductNotInBuffer(t *testing.T) {
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	product1 := uuid.New()
	product2 := uuid.New()
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts: []productForShip{{ProductID: product1}},
	}

	_, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: uuid.New(),
		DispatchID:  uuid.New(),
		OperatorID:  uuid.New(),
		ProductIDs:  []uuid.UUID{product1, product2},
	})
	if !errors.Is(err, ErrProductNotInBuffer) {
		t.Fatalf("expected ErrProductNotInBuffer, got %v", err)
	}
}

// TestShipPartialOrderKeepsOrderOpen проверяет, что частичная отгрузка не переводит заказ в SHIPPED.
func TestShipPartialOrderKeepsOrderOpen(t *testing.T) {
	bufferBinID := uuid.New()
	dispatchID := uuid.New()
	operatorID := uuid.New()
	destinationID := uuid.New()
	orderID := uuid.New()
	section := binSectionShippingBuffer
	productID := uuid.New()

	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			BinID:         bufferBinID,
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DispatchID:    dispatchID,
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts: []productForShip{{ProductID: productID, OrderID: &orderID}},
		// Repository returns 0 when the order still has non-SHIPPED products.
		ordersCompleted: 0,
		bufferRemaining: 0,
	}

	result, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: bufferBinID,
		DispatchID:  dispatchID,
		OperatorID:  operatorID,
		ProductIDs:  []uuid.UUID{productID},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ProductsShipped != 1 {
		t.Fatalf("expected 1 product shipped, got %d", result.ProductsShipped)
	}
	if result.OrdersCompleted != 0 {
		t.Fatalf("expected no completed orders, got %d", result.OrdersCompleted)
	}
	assertSameUUIDs(t, repo.updatedOrderIDs, []uuid.UUID{orderID})
	assertShippingEventsMatch(t, repo.insertedShippings, repo.insertedOutbox, []uuid.UUID{productID})
}

// TestShipDispatchDepartedWhenBufferEmpty проверяет перевод рейса в DEPARTED после очистки буфера.
func TestShipDispatchDepartedWhenBufferEmpty(t *testing.T) {
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts:    []productForShip{{ProductID: uuid.New()}},
		bufferRemaining: 0,
	}

	result, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: uuid.New(),
		DispatchID:  uuid.New(),
		OperatorID:  uuid.New(),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result.DispatchDeparted || !repo.dispatchDeparted {
		t.Fatal("expected dispatch departed")
	}
}

// TestShipDispatchNotDepartedWhenBufferHasRemainingProducts проверяет, что рейс остаётся у ворот, если в буфере остались товары.
func TestShipDispatchNotDepartedWhenBufferHasRemainingProducts(t *testing.T) {
	destinationID := uuid.New()
	section := binSectionShippingBuffer
	repo := &mockShippingRepo{
		bin: &bufferBinRecord{
			Section:       &section,
			DestinationID: &destinationID,
		},
		dispatch: &dispatchRecord{
			DestinationID: destinationID,
			Status:        domain.OutboundDispatchStatusAtGate,
		},
		shipProducts:    []productForShip{{ProductID: uuid.New()}},
		bufferRemaining: 1,
	}

	result, err := NewService(repo).Ship(context.Background(), ShipRequest{
		BufferBinID: uuid.New(),
		DispatchID:  uuid.New(),
		OperatorID:  uuid.New(),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.DispatchDeparted || repo.dispatchDeparted {
		t.Fatal("expected dispatch to stay at gate")
	}
	if result.BufferRemaining != 1 {
		t.Fatalf("expected 1 remaining product, got %d", result.BufferRemaining)
	}
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	return &id
}

// assertSameUUIDs проверяет, что два слайса UUID совпадают по длине и порядку элементов.
func assertSameUUIDs(t *testing.T, got, want []uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d ids, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected id[%d] %s, got %s", i, want[i], got[i])
		}
	}
}

// assertShippingEventsMatch проверяет, что shippings и outbox содержат одинаковые event_id для тех же товаров.
func assertShippingEventsMatch(t *testing.T, shippings, outbox []shippingEvent, productIDs []uuid.UUID) {
	t.Helper()
	if len(shippings) != len(productIDs) {
		t.Fatalf("expected %d shipping events, got %d", len(productIDs), len(shippings))
	}
	if len(outbox) != len(productIDs) {
		t.Fatalf("expected %d outbox events, got %d", len(productIDs), len(outbox))
	}
	for i := range productIDs {
		if shippings[i].ProductID != productIDs[i] {
			t.Fatalf("expected shipping product[%d] %s, got %s", i, productIDs[i], shippings[i].ProductID)
		}
		if outbox[i] != shippings[i] {
			t.Fatalf("expected outbox event[%d] to match shipping event", i)
		}
		if shippings[i].EventID == uuid.Nil {
			t.Fatalf("expected shipping event[%d] to have event id", i)
		}
	}
}
