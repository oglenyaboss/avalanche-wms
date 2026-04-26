package shipping

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wms/internal/domain"
)

const (
	binSectionShippingBuffer = "SHIPPING_BUFFER"
)

type Service struct {
	repo shippingRepository
}

type shippingRepository interface {
	GetBinWithDestinationByID(ctx context.Context, binID uuid.UUID) (*bufferBinRecord, error)
	ListReadyToShipProductsByBin(ctx context.Context, binID uuid.UUID) ([]readyToShipProduct, error)
	GetDispatchByCode(ctx context.Context, dispatchCode string) (*dispatchRecord, error)
	UpdateDispatchToAtGate(ctx context.Context, dispatchCode string) (*dispatchRecord, error)
}

func NewService(repo shippingRepository) *Service {
	return &Service{repo: repo}
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
		respProducts = append(respProducts, ScanBufferProductResponse{
			ProductID:       p.ProductID,
			QRCode:          p.QRCode,
			SKUName:         p.SKUName,
			OrderExternalNo: p.OrderExternalNo,
		})
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

func (d *dispatchRecord) toScanDriverResponse() *ScanDriverResponse {
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
