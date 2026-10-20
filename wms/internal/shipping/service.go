package shipping

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedops"
)

const binSectionShippingBuffer = "SHIPPING_BUFFER"

// Service отгружает буфер отгрузки на рейс: scan-buffer/scan-driver остаются
// беззнаковыми, сама отгрузка — подписанное действие (ADR-0001, #15), см. ship_signed.go.
type Service struct {
	repo   shippingRepository
	ops    operationStore
	signer signerSource
}

// shippingRepository — доступ к данным отгрузки; tx-методы (LockProductsForShip,
// LockOperation, LockSigner, WriteActions) вызывать только на репозитории из WithTx.
type shippingRepository interface {
	WithTx(ctx context.Context, fn func(shippingRepository) error) error
	GetBinWithDestinationByID(ctx context.Context, binID uuid.UUID) (*BufferBinRecord, error)
	GetBufferBinForUpdate(ctx context.Context, binID uuid.UUID) (*BufferBinRecord, error)
	ListReadyToShipProductsByBin(ctx context.Context, binID uuid.UUID) ([]ReadyToShipProduct, error)
	GetDispatchByCode(ctx context.Context, dispatchCode string) (*DispatchRecord, error)
	GetDispatchByID(ctx context.Context, dispatchID uuid.UUID) (*DispatchRecord, error)
	GetDispatchForUpdate(ctx context.Context, dispatchID uuid.UUID) (*DispatchRecord, error)
	UpdateDispatchToAtGate(ctx context.Context, dispatchCode string) (*DispatchRecord, error)
	ListProductsForShip(ctx context.Context, binID uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error)
	LockProductsForShip(ctx context.Context, binID uuid.UUID, productIDs []uuid.UUID) ([]ProductForShip, error)
	UnconfirmedProducts(ctx context.Context, productIDs []uuid.UUID, aggregate string) ([]uuid.UUID, error)
	BatchUpdateProductsShipped(ctx context.Context, productIDs []uuid.UUID) error
	BatchInsertShippings(ctx context.Context, events []shippingEvent, dispatchID, operatorID uuid.UUID, occurredAt time.Time, actionID uuid.UUID) error
	UpdateOrdersShippedConditional(ctx context.Context, orderIDs []uuid.UUID) (completed, partial int, err error)
	CountReadyToShipProductsInBuffer(ctx context.Context, binID uuid.UUID) (int, error)
	UpdateDispatchDeparted(ctx context.Context, dispatchID uuid.UUID) error
	LockOperation(ctx context.Context, operationID, createdBy uuid.UUID) (*signedops.Operation, error)
	LockSigner(ctx context.Context, credentialID []byte) error
	WriteActions(ctx context.Context, op *signedops.Operation, payloads [][]byte) error
}

func NewService(repo shippingRepository, ops operationStore, signer signerSource) *Service {
	return &Service{repo: repo, ops: ops, signer: signer}
}

func (s *Service) ScanBuffer(ctx context.Context, operatorID, bufferBinID uuid.UUID) (*ScanBufferResponse, error) {
	if operatorID == uuid.Nil || bufferBinID == uuid.Nil {
		return nil, fmt.Errorf("shipping.Service.ScanBuffer: %w", ErrInvalidInput)
	}

	bin, err := s.repo.GetBinWithDestinationByID(ctx, bufferBinID)
	if err != nil {
		return nil, fmt.Errorf("shipping.Service.ScanBuffer get bin: %w", err)
	}
	if bin.Section == nil || *bin.Section != binSectionShippingBuffer {
		return nil, fmt.Errorf("shipping.Service.ScanBuffer: %w", ErrBinNotShippingBuffer)
	}
	if bin.DestinationID == nil || bin.DestinationCode == nil || bin.DestinationName == nil {
		return nil, fmt.Errorf("shipping.Service.ScanBuffer destination: %w", ErrBinNotShippingBuffer)
	}

	products, err := s.repo.ListReadyToShipProductsByBin(ctx, bufferBinID)
	if err != nil {
		return nil, fmt.Errorf("shipping.Service.ScanBuffer list products: %w", err)
	}

	respProducts := make([]ScanBufferProductResponse, 0, len(products))
	for _, p := range products {
		respProducts = append(respProducts, ScanBufferProductResponse(p))
	}

	return &ScanBufferResponse{
		BufferBin: BufferBinResponse{
			ID:   bin.BinID,
			Code: bin.Code,
			Destination: DestinationResponse{
				ID:   *bin.DestinationID,
				Code: *bin.DestinationCode,
				Name: *bin.DestinationName,
			},
		},
		Products: respProducts,
		Count:    len(respProducts),
	}, nil
}

func (s *Service) ScanDriver(ctx context.Context, operatorID uuid.UUID, dispatchCode string) (*ScanDriverResponse, error) {
	if operatorID == uuid.Nil || dispatchCode == "" {
		return nil, fmt.Errorf("shipping.Service.ScanDriver: %w", ErrInvalidInput)
	}

	dispatch, err := s.repo.UpdateDispatchToAtGate(ctx, dispatchCode)
	if err == nil {
		return dispatch.toScanDriverResponse(), nil
	}
	if !errors.Is(err, ErrDispatchNotFound) {
		return nil, fmt.Errorf("shipping.Service.ScanDriver update dispatch: %w", err)
	}

	current, err := s.repo.GetDispatchByCode(ctx, dispatchCode)
	if err != nil {
		return nil, fmt.Errorf("shipping.Service.ScanDriver get dispatch: %w", err)
	}

	switch current.Status {
	case domain.OutboundDispatchStatusDeparted:
		return nil, fmt.Errorf("shipping.Service.ScanDriver: %w", ErrDispatchAlreadyDeparted)
	case domain.OutboundDispatchStatusCancelled:
		return nil, fmt.Errorf("shipping.Service.ScanDriver: %w", ErrDispatchCancelled)
	case domain.OutboundDispatchStatusAtGate:
		return current.toScanDriverResponse(), nil
	default:
		return nil, fmt.Errorf("shipping.Service.ScanDriver status %s: %w", current.Status, ErrInvalidInput)
	}
}

func (d *DispatchRecord) toScanDriverResponse() *ScanDriverResponse {
	return &ScanDriverResponse{
		DispatchID:    d.DispatchID,
		DispatchCode:  d.DispatchCode,
		VehicleNumber: d.VehicleNumber,
		DriverName:    d.DriverName,
		DriverPhone:   d.DriverPhone,
		Destination: DestinationResponse{
			ID:   d.DestinationID,
			Code: d.DestinationCode,
			Name: d.DestinationName,
		},
		Status:    d.Status,
		ArrivedAt: d.ArrivedAt,
	}
}
