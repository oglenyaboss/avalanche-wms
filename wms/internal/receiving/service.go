package receiving

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

const (
	shipmentStatusCreated        = "CREATED"
	shipmentStatusGateInProgress = "GATE_IN_PROGRESS"
	shipmentStatusGateClosed     = "GATE_CLOSED"

	cargoplaceStatusExpected        = "EXPECTED"
	cargoplaceStatusReceivedAtGate  = "RECEIVED_AT_GATE"
	cargoplaceStatusNotReceived     = "NOT_RECEIVED"
	cargoplaceStatusTableInProgress = "TABLE_IN_PROGRESS"
	cargoplaceStatusTableClosed     = "TABLE_CLOSED"

	boxStatusOpen   = "OPEN"
	boxStatusClosed = "CLOSED"
)

var (
	ErrTTNNotFound                 = errors.New("TTN_NOT_FOUND")
	ErrShipmentNotFound            = errors.New("SHIPMENT_NOT_FOUND")
	ErrShipmentAlreadyClosed       = errors.New("SHIPMENT_ALREADY_CLOSED")
	ErrShipmentNotInProgress       = errors.New("SHIPMENT_NOT_IN_PROGRESS")
	ErrCargoplaceNotInShipment     = errors.New("CARGOPLACE_NOT_IN_SHIPMENT")
	ErrCargoplaceAlreadyReceived   = errors.New("CARGOPLACE_ALREADY_RECEIVED")
	ErrInvalidInput                = errors.New("INVALID_INPUT")
	ErrCargoplaceNotFound          = errors.New("CARGOPLACE_NOT_FOUND")
	ErrCargoplaceNotReceivedAtGate = errors.New("CARGOPLACE_NOT_RECEIVED_AT_GATE")
	ErrCargoplaceNotInProgress     = errors.New("CARGOPLACE_NOT_IN_PROGRESS")
	ErrBoxNotFound                 = errors.New("BOX_NOT_FOUND")
	ErrBoxNotOpen                  = errors.New("BOX_NOT_OPEN")
	ErrBoxNotInCargoplace          = errors.New("BOX_NOT_IN_CARGOPLACE")
	ErrBarcodeNotFound             = errors.New("BARCODE_NOT_FOUND")
	ErrSKUNotFound                 = errors.New("SKU_NOT_FOUND")
	ErrQRAlreadyExists             = errors.New("QR_ALREADY_EXISTS")
	ErrBinNotFound                 = errors.New("BIN_NOT_FOUND")
	ErrBinNotBuffer                = errors.New("BIN_NOT_BUFFER")
)

type Service struct {
	repo receivingRepository
}

type receivingRepository interface {
	WithTx(ctx context.Context, fn func(receivingRepository) error) error
	GetShipmentByTTN(ctx context.Context, ttnCode string) (*domain.InboundShipment, error)
	GetShipmentByID(ctx context.Context, shipmentID uuid.UUID) (*domain.InboundShipment, error)
	ListCargoplacesByShipment(ctx context.Context, shipmentID uuid.UUID) ([]domain.Cargoplace, error)
	GetCargoplaceByShipmentAndCode(ctx context.Context, shipmentID uuid.UUID, cargoplaceCode string) (*domain.Cargoplace, error)
	GetCargoplaceByID(ctx context.Context, cargoplaceID uuid.UUID) (*domain.Cargoplace, error)
	UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, status string) error
	UpdateCargoplaceReceivedAtGate(ctx context.Context, cargoplaceID uuid.UUID, status string, receivedAt time.Time) error
	UpdateCargoplaceStatus(ctx context.Context, cargoplaceID uuid.UUID, status string) error
	MarkExpectedAsNotReceived(ctx context.Context, shipmentID uuid.UUID, notReceivedStatus string) error
	CountCargoplaces(ctx context.Context, shipmentID uuid.UUID) (int, error)
	CountCargoplacesByStatus(ctx context.Context, shipmentID uuid.UUID, status string) (int, error)
	ListExpectedSKUsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) ([]ExpectedSKU, error)
	UpsertBox(ctx context.Context, cargoplaceID uuid.UUID, boxBarcode string, status string) (*domain.Box, error)
	GetBoxByCargoplaceAndBarcode(ctx context.Context, cargoplaceID uuid.UUID, boxBarcode string) (*domain.Box, error)
	GetBoxByID(ctx context.Context, boxID uuid.UUID) (*domain.Box, error)
	UpdateBoxStatus(ctx context.Context, boxID uuid.UUID, status string) error
	CountProductsByBox(ctx context.Context, boxID uuid.UUID) (int, error)
	GetSKUByBarcode(ctx context.Context, barcode string) (*domain.SKU, error)
	GetSKUByID(ctx context.Context, skuID uuid.UUID) (*domain.SKU, error)
	InsertProduct(ctx context.Context, product *domain.Product) error
	GetBinByID(ctx context.Context, binID uuid.UUID) (*domain.Bin, error)
	MoveReceivedProductsToBuffer(ctx context.Context, cargoplaceID uuid.UUID, bufferBinID uuid.UUID) (int, error)
	ScanBufferWithLog(ctx context.Context, cargoplaceID uuid.UUID, bufferBinID uuid.UUID, logParams *TableLogParams) (int, error)
	CloseCargoplaceWithOutbox(ctx context.Context, params *CloseCargoplaceParams) (*CloseCargoplaceTxResult, error)
	CountProductsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error)
	CountExpectedItemsByCargoplace(ctx context.Context, cargoplaceID uuid.UUID) (int, error)
	InsertReceivingGateLog(ctx context.Context, params *GateLogParams) error
	InsertReceivingTableLog(ctx context.Context, params *TableLogParams) error
}

func NewService(repo receivingRepository) *Service {
	return &Service{repo: repo}
}

type ScanTTNResult struct {
	ShipmentID          uuid.UUID        `json:"shipment_id"`
	TTNCode             string           `json:"ttn_code"`
	Status              string           `json:"status"`
	Cargoplaces         []cargoplaceView `json:"cargoplaces"`
	TotalCargoplaces    int              `json:"total_cargoplaces"`
	ReceivedCargoplaces int              `json:"received_cargoplaces"`
}

type ScanGateCargoplaceResult struct {
	CargoplaceID     uuid.UUID `json:"cargoplace_id"`
	CargoplaceCode   string    `json:"cargoplace_code"`
	Status           string    `json:"status"`
	ReceivedAtGateAt time.Time `json:"received_at_gate_at"`
	Progress         progress  `json:"progress"`
}

type AcceptShipmentResult struct {
	ShipmentID uuid.UUID `json:"shipment_id"`
	Status     string    `json:"status"`
	Summary    progress  `json:"summary"`
}

type ScanTableCargoplaceResult struct {
	CargoplaceID   uuid.UUID     `json:"cargoplace_id"`
	CargoplaceCode string        `json:"cargoplace_code"`
	Status         string        `json:"status"`
	ExpectedSKUs   []ExpectedSKU `json:"expected_skus"`
	TotalExpected  int           `json:"total_expected"`
}

type ScanBoxResult struct {
	BoxID      uuid.UUID `json:"box_id"`
	BoxBarcode string    `json:"box_barcode"`
	Status     string    `json:"status"`
}

type ScanSKUResult struct {
	SKUID   uuid.UUID `json:"sku_id"`
	SKUName string    `json:"sku_name"`
	Barcode string    `json:"barcode"`
	Message string    `json:"message"`
}

type ScanQRResult struct {
	ProductID uuid.UUID            `json:"product_id"`
	SKUID     uuid.UUID            `json:"sku_id"`
	SKUName   string               `json:"sku_name"`
	QRCode    string               `json:"qr_code"`
	Status    domain.ProductStatus `json:"status"`
	Progress  receivingProgress    `json:"progress"`
}

type CloseBoxResult struct {
	BoxID         uuid.UUID `json:"box_id"`
	Status        string    `json:"status"`
	ProductsInBox int       `json:"products_in_box"`
}

type ScanBufferResult struct {
	BufferBinID    uuid.UUID `json:"buffer_bin_id"`
	BufferCode     string    `json:"buffer_code"`
	ProductsPlaced int       `json:"products_placed"`
}

type CloseCargoplaceResult struct {
	CargoplaceID        uuid.UUID              `json:"cargoplace_id"`
	Status              string                 `json:"status"`
	Summary             CloseCargoplaceSummary `json:"summary"`
	OutboxEventsCreated int                    `json:"outbox_events_created"`
}

type CloseCargoplaceSummary struct {
	ProductsReceived int             `json:"products_received"`
	ProductsExpected int             `json:"products_expected"`
	Shortage         int             `json:"shortage"`
	ShortageBySKU    []ShortageBySKU `json:"shortage_by_sku"`
}

type ExpectedSKU struct {
	SKUID       uuid.UUID `json:"sku_id"`
	SKUName     string    `json:"sku_name"`
	ExpectedQty int       `json:"expected_qty"`
}

type ReceivedSKUCount struct {
	SKUID       uuid.UUID `json:"sku_id"`
	SKUName     string    `json:"sku_name"`
	ReceivedQty int       `json:"received_qty"`
}

type ShortageBySKU struct {
	SKUName  string `json:"sku_name"`
	Expected int    `json:"expected"`
	Received int    `json:"received"`
	Shortage int    `json:"shortage"`
}

type CloseCargoplaceParams struct {
	CargoplaceID uuid.UUID
	OperatorID   uuid.UUID
	OccurredAt   time.Time
}

type CloseCargoplaceTxResult struct {
	ExpectedSKUs        []ExpectedSKU
	ReceivedSKUCounts   []ReceivedSKUCount
	OutboxEventsCreated int
}

type cargoplaceView struct {
	CargoplaceID   uuid.UUID `json:"cargoplace_id"`
	CargoplaceCode string    `json:"cargoplace_code"`
	Status         string    `json:"status"`
}

type progress struct {
	Total       int `json:"total"`
	Received    int `json:"received"`
	Remaining   int `json:"remaining"`
	NotReceived int `json:"not_received"`
}

type receivingProgress struct {
	ReceivedInCargoplace int `json:"received_in_cargoplace"`
	ExpectedInCargoplace int `json:"expected_in_cargoplace"`
}

func (s *Service) ScanTTN(ctx context.Context, operatorID uuid.UUID, ttnCode string) (*ScanTTNResult, error) {
	if operatorID == uuid.Nil || ttnCode == "" {
		return nil, fmt.Errorf("receiving.Service.ScanTTN: %w", ErrInvalidInput)
	}

	shipment, err := s.repo.GetShipmentByTTN(ctx, ttnCode)
	if err != nil {
		if errors.Is(err, ErrTTNNotFound) {
			return nil, fmt.Errorf("receiving.Service.ScanTTN: %w", err)
		}
		return nil, fmt.Errorf("receiving.Service.ScanTTN get shipment: %w", err)
	}

	if shipment.Status == shipmentStatusGateClosed {
		return nil, fmt.Errorf("receiving.Service.ScanTTN: %w", ErrShipmentAlreadyClosed)
	}

	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if shipment.Status == shipmentStatusCreated {
			// we switch to the GATE_IN_PROGRESS status at the first TTN scan
			if err := txRepo.UpdateShipmentStatus(ctx, shipment.ShipmentID, shipmentStatusGateInProgress); err != nil {
				return fmt.Errorf("receiving.Service.ScanTTN update shipment status: %w", err)
			}
			shipment.Status = shipmentStatusGateInProgress
		}

		if err := txRepo.InsertReceivingGateLog(ctx, &GateLogParams{
			TTNCode:    &shipment.TTNCode,
			ShipmentID: &shipment.ShipmentID,
			OperatorID: operatorID,
			Action:     "SCAN_TTN",
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.ScanTTN insert log: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	cargoplaces, err := s.repo.ListCargoplacesByShipment(ctx, shipment.ShipmentID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanTTN list cargoplaces: %w", err)
	}

	items := make([]cargoplaceView, 0, len(cargoplaces))
	received := 0
	for _, cp := range cargoplaces {
		items = append(items, cargoplaceView{
			CargoplaceID:   cp.CargoplaceID,
			CargoplaceCode: cp.CargoplaceCode,
			Status:         cp.Status,
		})
		if cp.Status == cargoplaceStatusReceivedAtGate {
			received++
		}
	}

	return &ScanTTNResult{
		ShipmentID:          shipment.ShipmentID,
		TTNCode:             shipment.TTNCode,
		Status:              shipment.Status,
		Cargoplaces:         items,
		TotalCargoplaces:    len(items),
		ReceivedCargoplaces: received,
	}, nil
}

func (s *Service) ScanCargoplace(
	ctx context.Context,
	operatorID uuid.UUID,
	shipmentID uuid.UUID,
	cargoplaceCode string,
) (*ScanGateCargoplaceResult, error) {
	if operatorID == uuid.Nil || shipmentID == uuid.Nil || cargoplaceCode == "" {
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace: %w", ErrInvalidInput)
	}

	shipment, err := s.repo.GetShipmentByID(ctx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace get shipment: %w", err)
	}
	if shipment.Status != shipmentStatusGateInProgress {
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace: %w", ErrShipmentNotInProgress)
	}

	cp, err := s.repo.GetCargoplaceByShipmentAndCode(ctx, shipmentID, cargoplaceCode)
	if err != nil {
		if errors.Is(err, ErrCargoplaceNotInShipment) {
			return nil, fmt.Errorf("receiving.Service.ScanCargoplace: %w", err)
		}
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace get cargoplace: %w", err)
	}

	if cp.Status == cargoplaceStatusReceivedAtGate {
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace: %w", ErrCargoplaceAlreadyReceived)
	}

	receivedAt := time.Now().UTC()
	var total, received int
	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if err := txRepo.UpdateCargoplaceReceivedAtGate(
			ctx,
			cp.CargoplaceID,
			cargoplaceStatusReceivedAtGate,
			receivedAt,
		); err != nil {
			return fmt.Errorf("receiving.Service.ScanCargoplace update cargoplace: %w", err)
		}

		if err := txRepo.InsertReceivingGateLog(ctx, &GateLogParams{
			TTNCode:        &shipment.TTNCode,
			CargoplaceCode: &cp.CargoplaceCode,
			ShipmentID:     &shipment.ShipmentID,
			CargoplaceID:   &cp.CargoplaceID,
			OperatorID:     operatorID,
			Action:         "SCAN_CARGOPLACE",
			OccurredAt:     receivedAt,
		}); err != nil {
			return fmt.Errorf("receiving.Service.ScanCargoplace insert log: %w", err)
		}

		total, received, err = s.countShipmentProgress(ctx, txRepo, shipmentID)
		if err != nil {
			return fmt.Errorf("receiving.Service.ScanCargoplace count progress: %w", err)
		}

		if err := s.tryAutoCloseShipment(ctx, txRepo, shipment.ShipmentID, shipment.TTNCode, total, received, operatorID); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &ScanGateCargoplaceResult{
		CargoplaceID:     cp.CargoplaceID,
		CargoplaceCode:   cp.CargoplaceCode,
		Status:           cargoplaceStatusReceivedAtGate,
		ReceivedAtGateAt: receivedAt,
		Progress: progress{
			Total:     total,
			Received:  received,
			Remaining: total - received,
		},
	}, nil
}

func (s *Service) AcceptShipment(
	ctx context.Context,
	operatorID uuid.UUID,
	shipmentID uuid.UUID,
) (*AcceptShipmentResult, error) {
	if operatorID == uuid.Nil || shipmentID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.AcceptShipment: %w", ErrInvalidInput)
	}

	shipment, err := s.repo.GetShipmentByID(ctx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.AcceptShipment get shipment: %w", err)
	}
	if shipment.Status != shipmentStatusGateInProgress {
		return nil, fmt.Errorf("receiving.Service.AcceptShipment: %w", ErrShipmentNotInProgress)
	}

	var total, received int
	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if err := txRepo.MarkExpectedAsNotReceived(ctx, shipmentID, cargoplaceStatusNotReceived); err != nil {
			return fmt.Errorf("receiving.Service.AcceptShipment mark not received: %w", err)
		}
		if err := txRepo.UpdateShipmentStatus(ctx, shipmentID, shipmentStatusGateClosed); err != nil {
			return fmt.Errorf("receiving.Service.AcceptShipment close shipment: %w", err)
		}

		if err := txRepo.InsertReceivingGateLog(ctx, &GateLogParams{
			TTNCode:    &shipment.TTNCode,
			ShipmentID: &shipmentID,
			OperatorID: operatorID,
			Action:     "SHIPMENT_ACCEPTED",
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.AcceptShipment insert log: %w", err)
		}

		total, received, err = s.countShipmentProgress(ctx, txRepo, shipmentID)
		if err != nil {
			return fmt.Errorf("receiving.Service.AcceptShipment count progress: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &AcceptShipmentResult{
		ShipmentID: shipmentID,
		Status:     shipmentStatusGateClosed,
		Summary: progress{
			Total:       total,
			Received:    received,
			NotReceived: total - received,
		},
	}, nil
}

func (s *Service) ScanTableCargoplace(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
) (*ScanTableCargoplaceResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.ScanTableCargoplace: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanTableCargoplace get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusReceivedAtGate {
		return nil, fmt.Errorf("receiving.Service.ScanTableCargoplace: %w", ErrCargoplaceNotReceivedAtGate)
	}

	expectedSKUs, err := s.repo.ListExpectedSKUsByCargoplace(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanTableCargoplace list expected skus: %w", err)
	}

	totalExpected := 0
	for _, expectedSKU := range expectedSKUs {
		totalExpected += expectedSKU.ExpectedQty
	}

	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if err := txRepo.UpdateCargoplaceStatus(ctx, cargoplaceID, cargoplaceStatusTableInProgress); err != nil {
			return fmt.Errorf("receiving.Service.ScanTableCargoplace update cargoplace status: %w", err)
		}

		if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
			CargoplaceID: cargoplaceID,
			OperatorID:   operatorID,
			Action:       "SCAN_CARGOPLACE",
			OccurredAt:   time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.ScanTableCargoplace insert log: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &ScanTableCargoplaceResult{
		CargoplaceID:   cargoplaceID,
		CargoplaceCode: cargoplace.CargoplaceCode,
		Status:         cargoplaceStatusTableInProgress,
		ExpectedSKUs:   expectedSKUs,
		TotalExpected:  totalExpected,
	}, nil
}

func (s *Service) ScanBox(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	boxBarcode string,
) (*ScanBoxResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil || boxBarcode == "" {
		return nil, fmt.Errorf("receiving.Service.ScanBox: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanBox get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Service.ScanBox: %w", ErrCargoplaceNotInProgress)
	}

	box, err := s.repo.GetBoxByCargoplaceAndBarcode(ctx, cargoplaceID, boxBarcode)
	switch {
	case err == nil:
		if box.Status != boxStatusOpen {
			return nil, fmt.Errorf("receiving.Service.ScanBox: %w", ErrBoxNotOpen)
		}

		if err := s.repo.InsertReceivingTableLog(ctx, &TableLogParams{
			CargoplaceID: cargoplaceID,
			BoxID:        &box.BoxID,
			OperatorID:   operatorID,
			Action:       "SCAN_BOX",
			BoxBarcode:   &boxBarcode,
			OccurredAt:   time.Now().UTC(),
		}); err != nil {
			return nil, fmt.Errorf("receiving.Service.ScanBox insert log: %w", err)
		}
	case errors.Is(err, ErrBoxNotFound):
		if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
			box, err = txRepo.UpsertBox(ctx, cargoplaceID, boxBarcode, boxStatusOpen)
			if err != nil {
				return fmt.Errorf("receiving.Service.ScanBox upsert box: %w", err)
			}

			if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
				CargoplaceID: cargoplaceID,
				BoxID:        &box.BoxID,
				OperatorID:   operatorID,
				Action:       "SCAN_BOX",
				BoxBarcode:   &boxBarcode,
				OccurredAt:   time.Now().UTC(),
			}); err != nil {
				return fmt.Errorf("receiving.Service.ScanBox insert log: %w", err)
			}

			return nil
		}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("receiving.Service.ScanBox get box by barcode: %w", err)
	}

	return &ScanBoxResult{
		BoxID:      box.BoxID,
		BoxBarcode: box.BoxBarcode,
		Status:     box.Status,
	}, nil
}

func (s *Service) ScanSKU(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	boxID uuid.UUID,
	barcode string,
) (*ScanSKUResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil || boxID == uuid.Nil || barcode == "" {
		return nil, fmt.Errorf("receiving.Service.ScanSKU: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanSKU get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Service.ScanSKU: %w", ErrCargoplaceNotInProgress)
	}

	box, err := s.repo.GetBoxByID(ctx, boxID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanSKU get box: %w", err)
	}
	if box.CargoplaceID != cargoplaceID {
		return nil, fmt.Errorf("receiving.Service.ScanSKU: %w", ErrBoxNotInCargoplace)
	}
	if box.Status != boxStatusOpen {
		return nil, fmt.Errorf("receiving.Service.ScanSKU: %w", ErrBoxNotOpen)
	}

	sku, err := s.repo.GetSKUByBarcode(ctx, barcode)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanSKU get sku by barcode: %w", err)
	}

	if err := s.repo.InsertReceivingTableLog(ctx, &TableLogParams{
		CargoplaceID: cargoplaceID,
		BoxID:        &boxID,
		OperatorID:   operatorID,
		Action:       "SCAN_SKU",
		SKUID:        &sku.SKUID,
		OccurredAt:   time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanSKU insert log: %w", err)
	}

	return &ScanSKUResult{
		SKUID:   sku.SKUID,
		SKUName: sku.Name,
		Barcode: barcode,
		Message: "Наклейте QR на товар",
	}, nil
}

func (s *Service) ScanQR(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	boxID uuid.UUID,
	skuID uuid.UUID,
	qrCode string,
) (*ScanQRResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil || boxID == uuid.Nil || skuID == uuid.Nil || qrCode == "" {
		return nil, fmt.Errorf("receiving.Service.ScanQR: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Service.ScanQR: %w", ErrCargoplaceNotInProgress)
	}

	box, err := s.repo.GetBoxByID(ctx, boxID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR get box: %w", err)
	}
	if box.CargoplaceID != cargoplaceID {
		return nil, fmt.Errorf("receiving.Service.ScanQR: %w", ErrBoxNotInCargoplace)
	}
	if box.Status != boxStatusOpen {
		return nil, fmt.Errorf("receiving.Service.ScanQR: %w", ErrBoxNotOpen)
	}

	sku, err := s.repo.GetSKUByID(ctx, skuID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR get sku: %w", err)
	}

	productID := uuid.New()
	product := &domain.Product{
		ProductID:    productID,
		SKUID:        sku.SKUID,
		ShipmentID:   cargoplace.ShipmentID,
		CargoplaceID: cargoplace.CargoplaceID,
		BoxID:        &boxID,
		QRCode:       qrCode,
		Status:       domain.ProductStatusReceived,
	}

	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if err := txRepo.InsertProduct(ctx, product); err != nil {
			return fmt.Errorf("receiving.Service.ScanQR insert product: %w", err)
		}

		if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
			CargoplaceID: cargoplaceID,
			BoxID:        &boxID,
			OperatorID:   operatorID,
			Action:       "SCAN_QR",
			SKUID:        &skuID,
			QRCode:       &qrCode,
			ProductID:    &productID,
			OccurredAt:   time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.ScanQR insert log: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	receivedInCargoplace, err := s.repo.CountProductsByCargoplace(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR count products in cargoplace: %w", err)
	}
	expectedInCargoplace, err := s.repo.CountExpectedItemsByCargoplace(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanQR count expected items: %w", err)
	}

	return &ScanQRResult{
		ProductID: productID,
		SKUID:     sku.SKUID,
		SKUName:   sku.Name,
		QRCode:    qrCode,
		Status:    domain.ProductStatusReceived,
		Progress: receivingProgress{
			ReceivedInCargoplace: receivedInCargoplace,
			ExpectedInCargoplace: expectedInCargoplace,
		},
	}, nil
}

func (s *Service) CloseBox(
	ctx context.Context,
	operatorID uuid.UUID,
	boxID uuid.UUID,
) (*CloseBoxResult, error) {
	if operatorID == uuid.Nil || boxID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.CloseBox: %w", ErrInvalidInput)
	}

	box, err := s.repo.GetBoxByID(ctx, boxID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.CloseBox get box: %w", err)
	}
	if box.Status != boxStatusOpen {
		return nil, fmt.Errorf("receiving.Service.CloseBox: %w", ErrBoxNotOpen)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, box.CargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.CloseBox get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Service.CloseBox: %w", ErrCargoplaceNotInProgress)
	}

	if err := s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if err := txRepo.UpdateBoxStatus(ctx, boxID, boxStatusClosed); err != nil {
			return fmt.Errorf("receiving.Service.CloseBox update box status: %w", err)
		}

		if err := txRepo.InsertReceivingTableLog(ctx, &TableLogParams{
			CargoplaceID: box.CargoplaceID,
			BoxID:        &boxID,
			OperatorID:   operatorID,
			Action:       "CLOSE_BOX",
			OccurredAt:   time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.CloseBox insert log: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	productsInBox, err := s.repo.CountProductsByBox(ctx, boxID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.CloseBox count products in box: %w", err)
	}

	return &CloseBoxResult{
		BoxID:         boxID,
		Status:        boxStatusClosed,
		ProductsInBox: productsInBox,
	}, nil
}

func (s *Service) ScanBuffer(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
	bufferBinID uuid.UUID,
) (*ScanBufferResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil || bufferBinID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.ScanBuffer: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanBuffer get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Service.ScanBuffer: %w", ErrCargoplaceNotInProgress)
	}

	bufferBin, err := s.repo.GetBinByID(ctx, bufferBinID)
	if err != nil {
		if errors.Is(err, ErrBinNotFound) {
			return nil, fmt.Errorf("receiving.Service.ScanBuffer: %w", err)
		}
		return nil, fmt.Errorf("receiving.Service.ScanBuffer get bin: %w", err)
	}
	if !isBufferBin(bufferBin) {
		return nil, fmt.Errorf("receiving.Service.ScanBuffer: %w", ErrBinNotBuffer)
	}

	productsPlaced, err := s.repo.ScanBufferWithLog(ctx, cargoplaceID, bufferBinID, &TableLogParams{
		CargoplaceID: cargoplaceID,
		OperatorID:   operatorID,
		Action:       "SCAN_BUFFER",
		BufferBinID:  &bufferBinID,
		OccurredAt:   time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanBuffer scan buffer with log: %w", err)
	}

	return &ScanBufferResult{
		BufferBinID:    bufferBinID,
		BufferCode:     bufferBin.Code,
		ProductsPlaced: productsPlaced,
	}, nil
}

func (s *Service) CloseCargoplace(
	ctx context.Context,
	operatorID uuid.UUID,
	cargoplaceID uuid.UUID,
) (*CloseCargoplaceResult, error) {
	if operatorID == uuid.Nil || cargoplaceID == uuid.Nil {
		return nil, fmt.Errorf("receiving.Service.CloseCargoplace: %w", ErrInvalidInput)
	}

	cargoplace, err := s.repo.GetCargoplaceByID(ctx, cargoplaceID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.CloseCargoplace get cargoplace: %w", err)
	}
	if cargoplace.Status != cargoplaceStatusTableInProgress {
		return nil, fmt.Errorf("receiving.Service.CloseCargoplace: %w", ErrCargoplaceNotInProgress)
	}

	occurredAt := time.Now().UTC()
	txResult, err := s.repo.CloseCargoplaceWithOutbox(ctx, &CloseCargoplaceParams{
		CargoplaceID: cargoplaceID,
		OperatorID:   operatorID,
		OccurredAt:   occurredAt,
	})
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.CloseCargoplace close cargoplace with outbox: %w", err)
	}

	summary := buildCloseCargoplaceSummary(txResult.ExpectedSKUs, txResult.ReceivedSKUCounts)

	return &CloseCargoplaceResult{
		CargoplaceID:        cargoplaceID,
		Status:              cargoplaceStatusTableClosed,
		Summary:             summary,
		OutboxEventsCreated: txResult.OutboxEventsCreated,
	}, nil
}

func (s *Service) countShipmentProgress(
	ctx context.Context,
	repo receivingRepository,
	shipmentID uuid.UUID,
) (total, received int, err error) {
	total, err = repo.CountCargoplaces(ctx, shipmentID)
	if err != nil {
		return 0, 0, fmt.Errorf("count total: %w", err)
	}

	received, err = repo.CountCargoplacesByStatus(ctx, shipmentID, cargoplaceStatusReceivedAtGate)
	if err != nil {
		return 0, 0, fmt.Errorf("count received: %w", err)
	}

	return total, received, nil
}

func (s *Service) tryAutoCloseShipment(
	ctx context.Context,
	repo receivingRepository,
	shipmentID uuid.UUID,
	ttnCode string,
	total, received int,
	operatorID uuid.UUID,
) error {
	if total == 0 || received < total {
		return nil
	}

	if err := repo.UpdateShipmentStatus(ctx, shipmentID, shipmentStatusGateClosed); err != nil {
		return fmt.Errorf("receiving.Service.tryAutoCloseShipment close shipment: %w", err)
	}

	if err := repo.InsertReceivingGateLog(ctx, &GateLogParams{
		TTNCode:    &ttnCode,
		ShipmentID: &shipmentID,
		OperatorID: operatorID,
		Action:     "SHIPMENT_ACCEPTED",
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("receiving.Service.tryAutoCloseShipment insert log: %w", err)
	}

	return nil
}

// buildCloseCargoplaceSummary compares the expected and received SKUs for a cargoplace and builds a summary including total counts and shortages by SKU.
func buildCloseCargoplaceSummary(expectedSKUs []ExpectedSKU, receivedSKUs []ReceivedSKUCount) CloseCargoplaceSummary {
	receivedBySKU := make(map[uuid.UUID]ReceivedSKUCount, len(receivedSKUs))
	totalReceived := 0
	for _, received := range receivedSKUs {
		receivedBySKU[received.SKUID] = received
		totalReceived += received.ReceivedQty
	}

	totalExpected := 0
	totalShortage := 0
	shortageBySKU := make([]ShortageBySKU, 0)
	for _, expected := range expectedSKUs {
		totalExpected += expected.ExpectedQty
		receivedQty := 0
		if received, ok := receivedBySKU[expected.SKUID]; ok {
			receivedQty = received.ReceivedQty
		}

		shortage := expected.ExpectedQty - receivedQty
		if shortage <= 0 {
			continue
		}

		totalShortage += shortage
		shortageBySKU = append(shortageBySKU, ShortageBySKU{
			SKUName:  expected.SKUName,
			Expected: expected.ExpectedQty,
			Received: receivedQty,
			Shortage: shortage,
		})
	}

	return CloseCargoplaceSummary{
		ProductsReceived: totalReceived,
		ProductsExpected: totalExpected,
		Shortage:         totalShortage,
		ShortageBySKU:    shortageBySKU,
	}
}

func isBufferBin(bin *domain.Bin) bool {
	if bin == nil || bin.Section == nil {
		return false
	}

	section := strings.ToUpper(strings.TrimSpace(*bin.Section))
	return strings.Contains(section, "BUFFER")
}

func payloadHashForReceiving(productID, cargoplaceID uuid.UUID) (string, error) {
	payload := struct {
		ProductID     uuid.UUID `json:"product_id"`
		CargoplaceID  uuid.UUID `json:"cargoplace_id"`
		AggregateType string    `json:"aggregate_type"`
		EventType     string    `json:"event_type"`
	}{
		ProductID:     productID,
		CargoplaceID:  cargoplaceID,
		AggregateType: "receiving",
		EventType:     "wms.receiving.v1",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("receiving.payloadHashForReceiving marshal payload: %w", err)
	}

	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
