package receiving

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

type mockReceivingRepo struct {
	shipmentByTTN                *domain.InboundShipment
	shipmentByTTNErr             error
	shipmentByID                 *domain.InboundShipment
	shipmentByIDErr              error
	cargoplaces                  []domain.Cargoplace
	listCargoplacesErr           error
	cargoplace                   *domain.Cargoplace
	cargoplaceErr                error
	cargoplaceByID               *domain.Cargoplace
	cargoplaceByIDErr            error
	updateShipmentStatusCalls    []string
	updateShipmentStatusErr      error
	updateCargoplaceID           uuid.UUID
	updateCargoplaceStatus       string
	updateCargoplaceReceivedAt   time.Time
	updateCargoplaceErr          error
	updateCargoplaceStatusID     uuid.UUID
	updateCargoplaceStatusValue  string
	updateCargoplaceStatusErr    error
	markNotReceivedShipmentID    uuid.UUID
	markNotReceivedStatus        string
	markNotReceivedErr           error
	countTotal                   int
	countTotalErr                error
	countByStatus                map[string]int
	countByStatusErr             error
	expectedSKUs                 []ExpectedSKU
	expectedSKUsErr              error
	box                          *domain.Box
	boxErr                       error
	boxByBarcode                 *domain.Box
	boxByBarcodeErr              error
	upsertedBox                  *domain.Box
	upsertBoxErr                 error
	upsertBoxCargoplaceID        uuid.UUID
	upsertBoxBarcode             string
	upsertBoxStatus              string
	updateBoxStatusID            uuid.UUID
	updateBoxStatusValue         string
	updateBoxStatusErr           error
	productsInBox                int
	productsInBoxErr             error
	skuByBarcode                 *domain.SKU
	skuByBarcodeErr              error
	skuByID                      *domain.SKU
	skuByIDErr                   error
	binByID                      *domain.Bin
	binByIDErr                   error
	insertedProduct              *domain.Product
	insertProductErr             error
	moveProductsToBufferCount    int
	moveProductsToBufferErr      error
	productsInCargoplace         int
	productsInCargoplaceErr      error
	expectedItemsInCargoplace    int
	expectedItemsInCargoplaceErr error
	closeCargoplaceTxResult      *CloseCargoplaceTxResult
	closeCargoplaceOutboxErr     error
	insertReceivingGateLogs      []GateLogParams
	insertReceivingGateLogErr    error
	insertReceivingTableLogs     []TableLogParams
	insertReceivingTableLogErr   error
	getCargoplaceByShipmentID    uuid.UUID
	getCargoplaceByShipmentCode  string
	listCargoplacesByShipmentID  uuid.UUID
	countCargoplacesShipmentID   uuid.UUID
	countByStatusShipmentID      uuid.UUID
	countByStatusRequestedStatus string
}

func (m *mockReceivingRepo) WithTx(_ context.Context, fn func(receivingRepository) error) error {
	return fn(m)
}

func (m *mockReceivingRepo) GetShipmentByTTN(_ context.Context, _ string) (*domain.InboundShipment, error) {
	if m.shipmentByTTNErr != nil {
		return nil, m.shipmentByTTNErr
	}
	return m.shipmentByTTN, nil
}

func (m *mockReceivingRepo) GetShipmentByID(_ context.Context, _ uuid.UUID) (*domain.InboundShipment, error) {
	if m.shipmentByIDErr != nil {
		return nil, m.shipmentByIDErr
	}
	return m.shipmentByID, nil
}

func (m *mockReceivingRepo) ListCargoplacesByShipment(
	_ context.Context,
	shipmentID uuid.UUID,
) ([]domain.Cargoplace, error) {
	m.listCargoplacesByShipmentID = shipmentID
	if m.listCargoplacesErr != nil {
		return nil, m.listCargoplacesErr
	}
	return m.cargoplaces, nil
}

func (m *mockReceivingRepo) GetCargoplaceByShipmentAndCode(
	_ context.Context,
	shipmentID uuid.UUID,
	cargoplaceCode string,
) (*domain.Cargoplace, error) {
	m.getCargoplaceByShipmentID = shipmentID
	m.getCargoplaceByShipmentCode = cargoplaceCode
	if m.cargoplaceErr != nil {
		return nil, m.cargoplaceErr
	}
	return m.cargoplace, nil
}

func (m *mockReceivingRepo) GetCargoplaceByID(_ context.Context, _ uuid.UUID) (*domain.Cargoplace, error) {
	if m.cargoplaceByIDErr != nil {
		return nil, m.cargoplaceByIDErr
	}
	return m.cargoplaceByID, nil
}

func (m *mockReceivingRepo) UpdateShipmentStatus(_ context.Context, shipmentID uuid.UUID, status string) error {
	m.updateShipmentStatusCalls = append(m.updateShipmentStatusCalls, shipmentID.String()+":"+status)
	if m.updateShipmentStatusErr != nil {
		return m.updateShipmentStatusErr
	}
	return nil
}

func (m *mockReceivingRepo) UpdateCargoplaceReceivedAtGate(
	_ context.Context,
	cargoplaceID uuid.UUID,
	status string,
	receivedAt time.Time,
) error {
	m.updateCargoplaceID = cargoplaceID
	m.updateCargoplaceStatus = status
	m.updateCargoplaceReceivedAt = receivedAt
	if m.updateCargoplaceErr != nil {
		return m.updateCargoplaceErr
	}
	return nil
}

func (m *mockReceivingRepo) UpdateCargoplaceStatus(_ context.Context, cargoplaceID uuid.UUID, status string) error {
	m.updateCargoplaceStatusID = cargoplaceID
	m.updateCargoplaceStatusValue = status
	if m.updateCargoplaceStatusErr != nil {
		return m.updateCargoplaceStatusErr
	}
	return nil
}

func (m *mockReceivingRepo) MarkExpectedAsNotReceived(
	_ context.Context,
	shipmentID uuid.UUID,
	notReceivedStatus string,
) error {
	m.markNotReceivedShipmentID = shipmentID
	m.markNotReceivedStatus = notReceivedStatus
	if m.markNotReceivedErr != nil {
		return m.markNotReceivedErr
	}
	return nil
}

func (m *mockReceivingRepo) CountCargoplaces(_ context.Context, shipmentID uuid.UUID) (int, error) {
	m.countCargoplacesShipmentID = shipmentID
	if m.countTotalErr != nil {
		return 0, m.countTotalErr
	}
	return m.countTotal, nil
}

func (m *mockReceivingRepo) CountCargoplacesByStatus(_ context.Context, shipmentID uuid.UUID, status string) (int, error) {
	m.countByStatusShipmentID = shipmentID
	m.countByStatusRequestedStatus = status
	if m.countByStatusErr != nil {
		return 0, m.countByStatusErr
	}
	return m.countByStatus[status], nil
}

func (m *mockReceivingRepo) ListExpectedSKUsByCargoplace(_ context.Context, _ uuid.UUID) ([]ExpectedSKU, error) {
	if m.expectedSKUsErr != nil {
		return nil, m.expectedSKUsErr
	}
	return m.expectedSKUs, nil
}

func (m *mockReceivingRepo) UpsertBox(_ context.Context, cargoplaceID uuid.UUID, boxBarcode string, status string) (*domain.Box, error) {
	m.upsertBoxCargoplaceID = cargoplaceID
	m.upsertBoxBarcode = boxBarcode
	m.upsertBoxStatus = status
	if m.upsertBoxErr != nil {
		return nil, m.upsertBoxErr
	}
	return m.upsertedBox, nil
}

func (m *mockReceivingRepo) GetBoxByID(_ context.Context, _ uuid.UUID) (*domain.Box, error) {
	if m.boxErr != nil {
		return nil, m.boxErr
	}
	return m.box, nil
}

func (m *mockReceivingRepo) GetBoxByCargoplaceAndBarcode(
	_ context.Context,
	_ uuid.UUID,
	_ string,
) (*domain.Box, error) {
	if m.boxByBarcodeErr != nil {
		return nil, m.boxByBarcodeErr
	}
	return m.boxByBarcode, nil
}

func (m *mockReceivingRepo) UpdateBoxStatus(_ context.Context, boxID uuid.UUID, status string) error {
	m.updateBoxStatusID = boxID
	m.updateBoxStatusValue = status
	if m.updateBoxStatusErr != nil {
		return m.updateBoxStatusErr
	}
	return nil
}

func (m *mockReceivingRepo) CountProductsByBox(_ context.Context, _ uuid.UUID) (int, error) {
	if m.productsInBoxErr != nil {
		return 0, m.productsInBoxErr
	}
	return m.productsInBox, nil
}

func (m *mockReceivingRepo) GetSKUByBarcode(_ context.Context, _ string) (*domain.SKU, error) {
	if m.skuByBarcodeErr != nil {
		return nil, m.skuByBarcodeErr
	}
	return m.skuByBarcode, nil
}

func (m *mockReceivingRepo) GetSKUByID(_ context.Context, _ uuid.UUID) (*domain.SKU, error) {
	if m.skuByIDErr != nil {
		return nil, m.skuByIDErr
	}
	return m.skuByID, nil
}

func (m *mockReceivingRepo) GetBinByID(_ context.Context, _ uuid.UUID) (*domain.Bin, error) {
	if m.binByIDErr != nil {
		return nil, m.binByIDErr
	}
	return m.binByID, nil
}

func (m *mockReceivingRepo) InsertProduct(_ context.Context, product *domain.Product) error {
	m.insertedProduct = product
	if m.insertProductErr != nil {
		return m.insertProductErr
	}
	return nil
}

func (m *mockReceivingRepo) CountProductsByCargoplace(_ context.Context, _ uuid.UUID) (int, error) {
	if m.productsInCargoplaceErr != nil {
		return 0, m.productsInCargoplaceErr
	}
	return m.productsInCargoplace, nil
}

func (m *mockReceivingRepo) MoveReceivedProductsToBuffer(_ context.Context, _ uuid.UUID, _ uuid.UUID) (int, error) {
	if m.moveProductsToBufferErr != nil {
		return 0, m.moveProductsToBufferErr
	}
	return m.moveProductsToBufferCount, nil
}

func (m *mockReceivingRepo) CountExpectedItemsByCargoplace(_ context.Context, _ uuid.UUID) (int, error) {
	if m.expectedItemsInCargoplaceErr != nil {
		return 0, m.expectedItemsInCargoplaceErr
	}
	return m.expectedItemsInCargoplace, nil
}

func (m *mockReceivingRepo) CloseCargoplaceWithOutbox(
	_ context.Context,
	_ *CloseCargoplaceParams,
) (*CloseCargoplaceTxResult, error) {
	if m.closeCargoplaceOutboxErr != nil {
		return nil, m.closeCargoplaceOutboxErr
	}
	return m.closeCargoplaceTxResult, nil
}

func (m *mockReceivingRepo) InsertReceivingGateLog(_ context.Context, params *GateLogParams) error {
	m.insertReceivingGateLogs = append(m.insertReceivingGateLogs, *params)
	if m.insertReceivingGateLogErr != nil {
		return m.insertReceivingGateLogErr
	}
	return nil
}

func (m *mockReceivingRepo) InsertReceivingTableLog(_ context.Context, params *TableLogParams) error {
	m.insertReceivingTableLogs = append(m.insertReceivingTableLogs, *params)
	if m.insertReceivingTableLogErr != nil {
		return m.insertReceivingTableLogErr
	}
	return nil
}

func TestServiceScanTTNTransitionsCreatedShipment(t *testing.T) {
	shipmentID := uuid.New()
	operatorID := uuid.New()
	repo := &mockReceivingRepo{
		shipmentByTTN: &domain.InboundShipment{
			ShipmentID: shipmentID,
			TTNCode:    "TTN-001",
			Status:     shipmentStatusCreated,
		},
		cargoplaces: []domain.Cargoplace{
			{CargoplaceID: uuid.New(), CargoplaceCode: "CP-001", Status: cargoplaceStatusExpected},
			{CargoplaceID: uuid.New(), CargoplaceCode: "CP-002", Status: cargoplaceStatusReceivedAtGate},
		},
	}

	result, err := NewService(repo).ScanTTN(context.Background(), operatorID, "TTN-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != shipmentStatusGateInProgress {
		t.Fatalf("expected status %s, got %s", shipmentStatusGateInProgress, result.Status)
	}
	if len(repo.updateShipmentStatusCalls) != 1 {
		t.Fatalf("expected one shipment status update, got %d", len(repo.updateShipmentStatusCalls))
	}
	if len(repo.insertReceivingGateLogs) != 1 || repo.insertReceivingGateLogs[0].Action != "SCAN_TTN" {
		t.Fatalf("expected SCAN_TTN log to be written, got %+v", repo.insertReceivingGateLogs)
	}
	if result.TotalCargoplaces != 2 || result.ReceivedCargoplaces != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
}

func TestServiceScanTTNRejectsClosedShipment(t *testing.T) {
	repo := &mockReceivingRepo{
		shipmentByTTN: &domain.InboundShipment{
			ShipmentID: uuid.New(),
			TTNCode:    "TTN-001",
			Status:     shipmentStatusGateClosed,
		},
	}

	_, err := NewService(repo).ScanTTN(context.Background(), uuid.New(), "TTN-001")
	if !errors.Is(err, ErrShipmentAlreadyClosed) {
		t.Fatalf("expected ErrShipmentAlreadyClosed, got %v", err)
	}
}

func TestServiceScanCargoplaceSuccess(t *testing.T) {
	shipmentID := uuid.New()
	cargoplaceID := uuid.New()
	operatorID := uuid.New()
	repo := &mockReceivingRepo{
		shipmentByID: &domain.InboundShipment{
			ShipmentID: shipmentID,
			TTNCode:    "TTN-001",
			Status:     shipmentStatusGateInProgress,
		},
		cargoplace: &domain.Cargoplace{
			CargoplaceID:   cargoplaceID,
			ShipmentID:     shipmentID,
			CargoplaceCode: "CP-001",
			Status:         cargoplaceStatusExpected,
		},
		countTotal: 3,
		countByStatus: map[string]int{
			cargoplaceStatusReceivedAtGate: 1,
		},
	}

	result, err := NewService(repo).ScanCargoplace(context.Background(), operatorID, shipmentID, "CP-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != cargoplaceStatusReceivedAtGate {
		t.Fatalf("expected status %s, got %s", cargoplaceStatusReceivedAtGate, result.Status)
	}
	if repo.updateCargoplaceID != cargoplaceID || repo.updateCargoplaceStatus != cargoplaceStatusReceivedAtGate {
		t.Fatalf("unexpected cargoplace update: %+v", repo)
	}
	if len(repo.insertReceivingGateLogs) != 1 || repo.insertReceivingGateLogs[0].Action != "SCAN_CARGOPLACE" {
		t.Fatalf("expected SCAN_CARGOPLACE log, got %+v", repo.insertReceivingGateLogs)
	}
	if result.Progress.Total != 3 || result.Progress.Received != 1 || result.Progress.Remaining != 2 {
		t.Fatalf("unexpected progress: %+v", result.Progress)
	}
}

func TestServiceScanCargoplaceRejectsAlreadyReceived(t *testing.T) {
	shipmentID := uuid.New()
	repo := &mockReceivingRepo{
		shipmentByID: &domain.InboundShipment{
			ShipmentID: shipmentID,
			TTNCode:    "TTN-001",
			Status:     shipmentStatusGateInProgress,
		},
		cargoplace: &domain.Cargoplace{
			CargoplaceID:   uuid.New(),
			ShipmentID:     shipmentID,
			CargoplaceCode: "CP-001",
			Status:         cargoplaceStatusReceivedAtGate,
		},
	}

	_, err := NewService(repo).ScanCargoplace(context.Background(), uuid.New(), shipmentID, "CP-001")
	if !errors.Is(err, ErrCargoplaceAlreadyReceive) {
		t.Fatalf("expected ErrCargoplaceAlreadyReceive, got %v", err)
	}
}

func TestServiceScanCargoplaceAutoClosesShipmentWhenAllReceived(t *testing.T) {
	shipmentID := uuid.New()
	operatorID := uuid.New()
	repo := &mockReceivingRepo{
		shipmentByID: &domain.InboundShipment{
			ShipmentID: shipmentID,
			TTNCode:    "TTN-002",
			Status:     shipmentStatusGateInProgress,
		},
		cargoplace: &domain.Cargoplace{
			CargoplaceID:   uuid.New(),
			ShipmentID:     shipmentID,
			CargoplaceCode: "CP-002",
			Status:         cargoplaceStatusExpected,
		},
		countTotal: 1,
		countByStatus: map[string]int{
			cargoplaceStatusReceivedAtGate: 1,
		},
	}

	_, err := NewService(repo).ScanCargoplace(context.Background(), operatorID, shipmentID, "CP-002")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(repo.updateShipmentStatusCalls) != 1 {
		t.Fatalf("expected auto-close shipment update, got %d", len(repo.updateShipmentStatusCalls))
	}
	if len(repo.insertReceivingGateLogs) != 2 {
		t.Fatalf("expected two logs, got %d", len(repo.insertReceivingGateLogs))
	}
	if repo.insertReceivingGateLogs[1].Action != "SHIPMENT_ACCEPTED" {
		t.Fatalf("expected SHIPMENT_ACCEPTED auto-close log, got %+v", repo.insertReceivingGateLogs[1])
	}
}

func TestServiceAcceptShipmentMarksMissingAsNotReceived(t *testing.T) {
	shipmentID := uuid.New()
	operatorID := uuid.New()
	repo := &mockReceivingRepo{
		shipmentByID: &domain.InboundShipment{
			ShipmentID: shipmentID,
			TTNCode:    "TTN-003",
			Status:     shipmentStatusGateInProgress,
		},
		countTotal: 4,
		countByStatus: map[string]int{
			cargoplaceStatusReceivedAtGate: 3,
		},
	}

	result, err := NewService(repo).AcceptShipment(context.Background(), operatorID, shipmentID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.markNotReceivedShipmentID != shipmentID || repo.markNotReceivedStatus != cargoplaceStatusNotReceived {
		t.Fatalf("expected missing cargoplaces to be marked NOT_RECEIVED, got %+v", repo)
	}
	if len(repo.updateShipmentStatusCalls) != 1 {
		t.Fatalf("expected shipment close update, got %d", len(repo.updateShipmentStatusCalls))
	}
	if len(repo.insertReceivingGateLogs) != 1 || repo.insertReceivingGateLogs[0].Action != "SHIPMENT_ACCEPTED" {
		t.Fatalf("expected SHIPMENT_ACCEPTED log, got %+v", repo.insertReceivingGateLogs)
	}
	if result.Status != shipmentStatusGateClosed || result.Summary.NotReceived != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestServiceAcceptShipmentRejectsShipmentOutsideGateProgress(t *testing.T) {
	repo := &mockReceivingRepo{
		shipmentByID: &domain.InboundShipment{
			ShipmentID: uuid.New(),
			TTNCode:    "TTN-004",
			Status:     shipmentStatusCreated,
		},
	}

	_, err := NewService(repo).AcceptShipment(context.Background(), uuid.New(), repo.shipmentByID.ShipmentID)
	if !errors.Is(err, ErrShipmentNotInProgress) {
		t.Fatalf("expected ErrShipmentNotInProgress, got %v", err)
	}
}

func TestServiceScanTableCargoplaceSuccess(t *testing.T) {
	cargoplaceID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID:   cargoplaceID,
			CargoplaceCode: "CP-001",
			Status:         cargoplaceStatusReceivedAtGate,
		},
		expectedSKUs: []ExpectedSKU{
			{SKUID: uuid.New(), SKUName: "SKU-A", ExpectedQty: 2},
			{SKUID: uuid.New(), SKUName: "SKU-B", ExpectedQty: 3},
		},
	}

	result, err := NewService(repo).ScanTableCargoplace(context.Background(), uuid.New(), cargoplaceID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != cargoplaceStatusTableInProgress {
		t.Fatalf("expected status %s, got %s", cargoplaceStatusTableInProgress, result.Status)
	}
	if result.TotalExpected != 5 {
		t.Fatalf("expected total_expected 5, got %d", result.TotalExpected)
	}
	if repo.updateCargoplaceStatusID != cargoplaceID || repo.updateCargoplaceStatusValue != cargoplaceStatusTableInProgress {
		t.Fatalf("unexpected cargoplace status update: %+v", repo)
	}
	if len(repo.insertReceivingTableLogs) != 1 || repo.insertReceivingTableLogs[0].Action != "SCAN_CARGOPLACE" {
		t.Fatalf("expected SCAN_CARGOPLACE log, got %+v", repo.insertReceivingTableLogs)
	}
}

func TestServiceScanSKURejectsClosedBox(t *testing.T) {
	cargoplaceID := uuid.New()
	boxID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableInProgress,
		},
		box: &domain.Box{
			BoxID:        boxID,
			CargoplaceID: cargoplaceID,
			Status:       boxStatusClosed,
		},
	}

	_, err := NewService(repo).ScanSKU(context.Background(), uuid.New(), cargoplaceID, boxID, "4607036430014")
	if !errors.Is(err, ErrBoxNotOpen) {
		t.Fatalf("expected ErrBoxNotOpen, got %v", err)
	}
}

func TestServiceScanBoxRejectsClosedExistingBox(t *testing.T) {
	cargoplaceID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableInProgress,
		},
		boxByBarcode: &domain.Box{
			BoxID:        uuid.New(),
			CargoplaceID: cargoplaceID,
			BoxBarcode:   "BOX-TABLE-001",
			Status:       boxStatusClosed,
		},
	}

	_, err := NewService(repo).ScanBox(context.Background(), uuid.New(), cargoplaceID, "BOX-TABLE-001")
	if !errors.Is(err, ErrBoxNotOpen) {
		t.Fatalf("expected ErrBoxNotOpen, got %v", err)
	}
}

func TestServiceScanQRSuccess(t *testing.T) {
	cargoplaceID := uuid.New()
	shipmentID := uuid.New()
	boxID := uuid.New()
	skuID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			ShipmentID:   shipmentID,
			Status:       cargoplaceStatusTableInProgress,
		},
		box: &domain.Box{
			BoxID:        boxID,
			CargoplaceID: cargoplaceID,
			Status:       boxStatusOpen,
		},
		skuByID: &domain.SKU{
			SKUID: skuID,
			Name:  "Ноутбук Lenovo X1",
		},
		productsInCargoplace:      5,
		expectedItemsInCargoplace: 12,
	}

	result, err := NewService(repo).ScanQR(context.Background(), uuid.New(), cargoplaceID, boxID, skuID, "WMS-QR-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.insertedProduct == nil {
		t.Fatal("expected product to be inserted")
	}
	if repo.insertedProduct.ShipmentID != shipmentID || repo.insertedProduct.CargoplaceID != cargoplaceID {
		t.Fatalf("unexpected product payload: %+v", repo.insertedProduct)
	}
	if result.Status != domain.ProductStatusReceived {
		t.Fatalf("expected status RECEIVED, got %s", result.Status)
	}
	if result.Progress.ReceivedInCargoplace != 5 || result.Progress.ExpectedInCargoplace != 12 {
		t.Fatalf("unexpected progress: %+v", result.Progress)
	}
	if len(repo.insertReceivingTableLogs) != 1 || repo.insertReceivingTableLogs[0].Action != "SCAN_QR" {
		t.Fatalf("expected SCAN_QR log, got %+v", repo.insertReceivingTableLogs)
	}
}

func TestServiceCloseBoxSuccess(t *testing.T) {
	boxID := uuid.New()
	cargoplaceID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableInProgress,
		},
		box: &domain.Box{
			BoxID:        boxID,
			CargoplaceID: cargoplaceID,
			Status:       boxStatusOpen,
		},
		productsInBox: 3,
	}

	result, err := NewService(repo).CloseBox(context.Background(), uuid.New(), boxID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.updateBoxStatusID != boxID || repo.updateBoxStatusValue != boxStatusClosed {
		t.Fatalf("unexpected box update: %+v", repo)
	}
	if result.ProductsInBox != 3 || result.Status != boxStatusClosed {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(repo.insertReceivingTableLogs) != 1 || repo.insertReceivingTableLogs[0].Action != "CLOSE_BOX" {
		t.Fatalf("expected CLOSE_BOX log, got %+v", repo.insertReceivingTableLogs)
	}
}

func TestServiceCloseBoxRejectsClosedBox(t *testing.T) {
	boxID := uuid.New()
	repo := &mockReceivingRepo{
		box: &domain.Box{
			BoxID:        boxID,
			CargoplaceID: uuid.New(),
			Status:       boxStatusClosed,
		},
	}

	_, err := NewService(repo).CloseBox(context.Background(), uuid.New(), boxID)
	if !errors.Is(err, ErrBoxNotOpen) {
		t.Fatalf("expected ErrBoxNotOpen, got %v", err)
	}
}

func TestServiceCloseBoxRejectsCargoplaceOutsideProgress(t *testing.T) {
	boxID := uuid.New()
	cargoplaceID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableClosed,
		},
		box: &domain.Box{
			BoxID:        boxID,
			CargoplaceID: cargoplaceID,
			Status:       boxStatusOpen,
		},
	}

	_, err := NewService(repo).CloseBox(context.Background(), uuid.New(), boxID)
	if !errors.Is(err, ErrCargoplaceNotInProgress) {
		t.Fatalf("expected ErrCargoplaceNotInProgress, got %v", err)
	}
}

func TestServiceScanBufferSuccess(t *testing.T) {
	cargoplaceID := uuid.New()
	bufferBinID := uuid.New()
	section := "BUFFER"
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableInProgress,
		},
		binByID: &domain.Bin{
			BinID:   bufferBinID,
			Code:    "BUFFER-01",
			Section: &section,
		},
		moveProductsToBufferCount: 4,
	}

	result, err := NewService(repo).ScanBuffer(context.Background(), uuid.New(), cargoplaceID, bufferBinID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.BufferCode != "BUFFER-01" || result.ProductsPlaced != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(repo.insertReceivingTableLogs) != 1 || repo.insertReceivingTableLogs[0].Action != "SCAN_BUFFER" {
		t.Fatalf("expected SCAN_BUFFER log, got %+v", repo.insertReceivingTableLogs)
	}
	if repo.insertReceivingTableLogs[0].BufferBinID == nil || *repo.insertReceivingTableLogs[0].BufferBinID != bufferBinID {
		t.Fatalf("expected buffer_bin_id to be logged, got %+v", repo.insertReceivingTableLogs[0])
	}
}

func TestServiceScanBufferRejectsNonBufferBin(t *testing.T) {
	cargoplaceID := uuid.New()
	section := "A"
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableInProgress,
		},
		binByID: &domain.Bin{
			BinID:   uuid.New(),
			Code:    "A-01-01",
			Section: &section,
		},
	}

	_, err := NewService(repo).ScanBuffer(context.Background(), uuid.New(), cargoplaceID, repo.binByID.BinID)
	if !errors.Is(err, ErrBinNotBuffer) {
		t.Fatalf("expected ErrBinNotBuffer, got %v", err)
	}
}

func TestServiceCloseCargoplaceSuccess(t *testing.T) {
	cargoplaceID := uuid.New()
	skuAID := uuid.New()
	skuBID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableInProgress,
		},
		closeCargoplaceTxResult: &CloseCargoplaceTxResult{
			ExpectedSKUs: []ExpectedSKU{
				{SKUID: skuAID, SKUName: "SKU-A", ExpectedQty: 5},
				{SKUID: skuBID, SKUName: "SKU-B", ExpectedQty: 3},
			},
			ReceivedSKUCounts: []ReceivedSKUCount{
				{SKUID: skuAID, SKUName: "SKU-A", ReceivedQty: 4},
				{SKUID: skuBID, SKUName: "SKU-B", ReceivedQty: 3},
			},
			OutboxEventsCreated: 7,
		},
	}

	result, err := NewService(repo).CloseCargoplace(context.Background(), uuid.New(), cargoplaceID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != cargoplaceStatusTableClosed {
		t.Fatalf("expected TABLE_CLOSED, got %s", result.Status)
	}
	if result.Summary.ProductsExpected != 8 || result.Summary.ProductsReceived != 7 || result.Summary.Shortage != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if len(result.Summary.ShortageBySKU) != 1 || result.Summary.ShortageBySKU[0].SKUName != "SKU-A" {
		t.Fatalf("unexpected shortage breakdown: %+v", result.Summary.ShortageBySKU)
	}
	if result.OutboxEventsCreated != 7 {
		t.Fatalf("expected 7 outbox events, got %d", result.OutboxEventsCreated)
	}
}

func TestServiceCloseCargoplaceRejectsStatusOutsideProgress(t *testing.T) {
	cargoplaceID := uuid.New()
	repo := &mockReceivingRepo{
		cargoplaceByID: &domain.Cargoplace{
			CargoplaceID: cargoplaceID,
			Status:       cargoplaceStatusTableClosed,
		},
	}

	_, err := NewService(repo).CloseCargoplace(context.Background(), uuid.New(), cargoplaceID)
	if !errors.Is(err, ErrCargoplaceNotInProgress) {
		t.Fatalf("expected ErrCargoplaceNotInProgress, got %v", err)
	}
}
