package receiving

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

const (
	shipmentStatusCreated        = "CREATED"
	shipmentStatusGateInProgress = "GATE_IN_PROGRESS"
	shipmentStatusGateClosed     = "GATE_CLOSED"

	cargoplaceStatusExpected       = "EXPECTED"
	cargoplaceStatusReceivedAtGate = "RECEIVED_AT_GATE"
	cargoplaceStatusNotReceived    = "NOT_RECEIVED"
)

var (
	ErrTTNNotFound              = errors.New("TTN_NOT_FOUND")
	ErrShipmentAlreadyClosed    = errors.New("SHIPMENT_ALREADY_CLOSED")
	ErrShipmentNotInProgress    = errors.New("SHIPMENT_NOT_IN_PROGRESS")
	ErrCargoplaceNotInShipment  = errors.New("CARGOPLACE_NOT_IN_SHIPMENT")
	ErrCargoplaceAlreadyReceive = errors.New("CARGOPLACE_ALREADY_RECEIVED")
	ErrInvalidInput             = errors.New("INVALID_INPUT")
)

type Service struct {
	repo receivingRepository
}

type receivingRepository interface {
	WithTx(ctx context.Context, fn func(receivingRepository) error) error
	GetShipmentByTTN(ctx context.Context, ttnCode string) (*domain.InboundShipment, error)
	GetShipmentByID(ctx context.Context, shipmentID uuid.UUID) (*domain.InboundShipment, error)
	ListCargoplacesByShipment(ctx context.Context, shipmentID uuid.UUID) ([]domain.Cargoplace, error)
	GetCargoplaceByShipmentAndCode(
		ctx context.Context,
		shipmentID uuid.UUID,
		cargoplaceCode string,
	) (*domain.Cargoplace, error)
	UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, status string) error
	UpdateCargoplaceReceivedAtGate(ctx context.Context, cargoplaceID uuid.UUID, status string, receivedAt time.Time) error
	MarkExpectedAsNotReceived(ctx context.Context, shipmentID uuid.UUID, notReceivedStatus string) error
	CountCargoplaces(ctx context.Context, shipmentID uuid.UUID) (int, error)
	CountCargoplacesByStatus(ctx context.Context, shipmentID uuid.UUID, status string) (int, error)
	InsertReceivingGateLog(ctx context.Context, params *GateLogParams) error
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

type ScanCargoplaceResult struct {
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

type cargoplaceView struct {
	CargoplaceID   uuid.UUID `json:"cargoplace_id"`
	CargoplaceCode string    `json:"cargoplace_code"`
	Status         string    `json:"status"`
}

type progress struct {
	Total       int `json:"total"`
	Received    int `json:"received"`
	Remaining   int `json:"remaining,omitempty"`
	NotReceived int `json:"not_received,omitempty"`
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
	// Load cargoplaces for response payload after shipment state transition is persisted.
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
) (*ScanCargoplaceResult, error) {
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
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace: %w", ErrCargoplaceAlreadyReceive)
	}

	receivedAt := time.Now().UTC()
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

		return nil
	}); err != nil {
		return nil, err
	}

	total, err := s.repo.CountCargoplaces(ctx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace count total: %w", err)
	}
	received, err := s.repo.CountCargoplacesByStatus(ctx, shipmentID, cargoplaceStatusReceivedAtGate)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.ScanCargoplace count received: %w", err)
	}

	return &ScanCargoplaceResult{
		CargoplaceID:     cp.CargoplaceID,
		CargoplaceCode:   cp.CargoplaceCode,
		Status:           cargoplaceStatusReceivedAtGate,
		ReceivedAtGateAt: receivedAt,
		Progress: progress{
			Total:     total,
			Received:  received,
			Remaining: total - received,
		},
	}, s.tryAutoCloseShipment(ctx, shipment.ShipmentID, shipment.TTNCode, total, received, operatorID)
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

		return nil
	}); err != nil {
		return nil, err
	}

	total, err := s.repo.CountCargoplaces(ctx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.AcceptShipment count total: %w", err)
	}
	received, err := s.repo.CountCargoplacesByStatus(ctx, shipmentID, cargoplaceStatusReceivedAtGate)
	if err != nil {
		return nil, fmt.Errorf("receiving.Service.AcceptShipment count received: %w", err)
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

func (s *Service) tryAutoCloseShipment(
	ctx context.Context,
	shipmentID uuid.UUID,
	ttnCode string,
	total, received int,
	operatorID uuid.UUID,
) error {
	if total == 0 || received < total {
		return nil
	}

	return s.repo.WithTx(ctx, func(txRepo receivingRepository) error {
		if err := txRepo.UpdateShipmentStatus(ctx, shipmentID, shipmentStatusGateClosed); err != nil {
			return fmt.Errorf("receiving.Service.tryAutoCloseShipment close shipment: %w", err)
		}

		if err := txRepo.InsertReceivingGateLog(ctx, &GateLogParams{
			TTNCode:    &ttnCode,
			ShipmentID: &shipmentID,
			OperatorID: operatorID,
			Action:     "SHIPMENT_ACCEPTED",
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("receiving.Service.tryAutoCloseShipment insert log: %w", err)
		}

		return nil
	})
}
