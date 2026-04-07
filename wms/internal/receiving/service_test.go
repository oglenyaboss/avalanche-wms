package receiving

import (
	"context"
	"encoding/json"
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
	updateShipmentStatusCalls    []string
	updateShipmentStatusErr      error
	updateCargoplaceID           uuid.UUID
	updateCargoplaceStatus       string
	updateCargoplaceReceivedAt   time.Time
	updateCargoplaceErr          error
	markNotReceivedShipmentID    uuid.UUID
	markNotReceivedStatus        string
	markNotReceivedErr           error
	countTotal                   int
	countTotalErr                error
	countByStatus                map[string]int
	countByStatusErr             error
	insertReceivingGateLogs      []GateLogParams
	insertReceivingGateLogErr    error
	getCargoplaceByShipmentID    uuid.UUID
	getCargoplaceByShipmentCode  string
	listCargoplacesByShipmentID  uuid.UUID
	countCargoplacesShipmentID   uuid.UUID
	countByStatusShipmentID      uuid.UUID
	countByStatusRequestedStatus string
	withTxCalls                  int
	inTx                         bool
	countTotalCalledInTx         bool
	countByStatusCalledInTx      bool
	insertReceivingGateLogCalls  int
	insertLogErrOnCall           int
}

func (m *mockReceivingRepo) WithTx(_ context.Context, fn func(receivingRepository) error) error {
	m.withTxCalls++
	shipmentByTTNBefore := cloneShipment(m.shipmentByTTN)
	shipmentByIDBefore := cloneShipment(m.shipmentByID)
	cargoplaceBefore := cloneCargoplace(m.cargoplace)
	m.inTx = true
	defer func() {
		m.inTx = false
	}()
	if err := fn(m); err != nil {
		m.shipmentByTTN = shipmentByTTNBefore
		m.shipmentByID = shipmentByIDBefore
		m.cargoplace = cargoplaceBefore
		return err
	}
	return nil
}

func cloneShipment(shipment *domain.InboundShipment) *domain.InboundShipment {
	if shipment == nil {
		return nil
	}

	copy := *shipment
	return &copy
}

func cloneCargoplace(cp *domain.Cargoplace) *domain.Cargoplace {
	if cp == nil {
		return nil
	}

	copy := *cp
	return &copy
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

func (m *mockReceivingRepo) UpdateShipmentStatus(_ context.Context, shipmentID uuid.UUID, status string) error {
	m.updateShipmentStatusCalls = append(m.updateShipmentStatusCalls, shipmentID.String()+":"+status)
	if m.updateShipmentStatusErr != nil {
		return m.updateShipmentStatusErr
	}
	if m.shipmentByTTN != nil && m.shipmentByTTN.ShipmentID == shipmentID {
		m.shipmentByTTN.Status = status
	}
	if m.shipmentByID != nil && m.shipmentByID.ShipmentID == shipmentID {
		m.shipmentByID.Status = status
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
	if m.cargoplace != nil && m.cargoplace.CargoplaceID == cargoplaceID {
		m.cargoplace.Status = status
		m.cargoplace.ReceivedAtGateAt = &receivedAt
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
	if m.inTx {
		m.countTotalCalledInTx = true
	}
	if m.countTotalErr != nil {
		return 0, m.countTotalErr
	}
	return m.countTotal, nil
}

func (m *mockReceivingRepo) CountCargoplacesByStatus(_ context.Context, shipmentID uuid.UUID, status string) (int, error) {
	m.countByStatusShipmentID = shipmentID
	m.countByStatusRequestedStatus = status
	if m.inTx {
		m.countByStatusCalledInTx = true
	}
	if m.countByStatusErr != nil {
		return 0, m.countByStatusErr
	}
	return m.countByStatus[status], nil
}

func (m *mockReceivingRepo) InsertReceivingGateLog(_ context.Context, params *GateLogParams) error {
	m.insertReceivingGateLogCalls++
	if m.insertLogErrOnCall > 0 && m.insertReceivingGateLogCalls == m.insertLogErrOnCall {
		return errors.New("forced log failure")
	}
	m.insertReceivingGateLogs = append(m.insertReceivingGateLogs, *params)
	if m.insertReceivingGateLogErr != nil {
		return m.insertReceivingGateLogErr
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
	if !repo.countTotalCalledInTx || !repo.countByStatusCalledInTx {
		t.Fatalf("expected progress counts inside transaction, got %+v", repo)
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
	if !errors.Is(err, ErrCargoplaceAlreadyReceived) {
		t.Fatalf("expected ErrCargoplaceAlreadyReceived, got %v", err)
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
	if repo.withTxCalls != 1 {
		t.Fatalf("expected auto-close to use the existing transaction, got %d transactions", repo.withTxCalls)
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
	if !repo.countTotalCalledInTx || !repo.countByStatusCalledInTx {
		t.Fatalf("expected summary counts inside transaction, got %+v", repo)
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

func TestServiceScanCargoplaceReturnsShipmentNotFound(t *testing.T) {
	repo := &mockReceivingRepo{
		shipmentByIDErr: ErrShipmentNotFound,
	}

	_, err := NewService(repo).ScanCargoplace(context.Background(), uuid.New(), uuid.New(), "CP-001")
	if !errors.Is(err, ErrShipmentNotFound) {
		t.Fatalf("expected ErrShipmentNotFound, got %v", err)
	}
}

func TestProgressJSONIncludesZeroValues(t *testing.T) {
	payload, err := json.Marshal(progress{
		Total:       1,
		Received:    1,
		Remaining:   0,
		NotReceived: 0,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(payload, &actual); err != nil {
		t.Fatalf("expected valid json, got %v", err)
	}

	if _, ok := actual["remaining"]; !ok {
		t.Fatalf("expected remaining to be present, got %s", payload)
	}
	if _, ok := actual["not_received"]; !ok {
		t.Fatalf("expected not_received to be present, got %s", payload)
	}
}
