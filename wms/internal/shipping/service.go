package shipping

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wms/internal/domain"
)

const (
	binSectionShippingBuffer = "SHIPPING_BUFFER"
	shippingAggregateType    = "shipping"
	shippingEventType        = "wms.shipping.v1"
)

type Service struct {
	repo shippingRepository
}

type shippingRepository interface {
	WithTx(ctx context.Context, fn func(shippingRepository) error) error
	GetBinWithDestinationByID(ctx context.Context, binID uuid.UUID) (*bufferBinRecord, error)
	GetBufferBinForUpdate(ctx context.Context, binID uuid.UUID) (*bufferBinRecord, error)
	ListReadyToShipProductsByBin(ctx context.Context, binID uuid.UUID) ([]readyToShipProduct, error)
	GetDispatchByCode(ctx context.Context, dispatchCode string) (*dispatchRecord, error)
	GetDispatchForUpdate(ctx context.Context, dispatchID uuid.UUID) (*dispatchRecord, error)
	UpdateDispatchToAtGate(ctx context.Context, dispatchCode string) (*dispatchRecord, error)
	SelectProductsForShip(ctx context.Context, binID uuid.UUID, productIDs []uuid.UUID) ([]productForShip, error)
	BatchUpdateProductsShipped(ctx context.Context, productIDs []uuid.UUID) error
	BatchInsertShippings(ctx context.Context, events []shippingEvent, dispatchID, operatorID uuid.UUID) error
	BatchInsertShippingOutbox(ctx context.Context, events []shippingEvent, dispatchID uuid.UUID) error
	UpdateOrdersShippedConditional(ctx context.Context, orderIDs []uuid.UUID) (int, error)
	CountReadyToShipProductsInBuffer(ctx context.Context, binID uuid.UUID) (int, error)
	UpdateDispatchDeparted(ctx context.Context, dispatchID uuid.UUID) error
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

func (s *Service) Ship(ctx context.Context, req ShipRequest) (*ShipResponse, error) {
	if req.OperatorID == uuid.Nil || req.BufferBinID == uuid.Nil || req.DispatchID == uuid.Nil {
		return nil, fmt.Errorf("shipping.Service.Ship: %w", ErrInvalidInput)
	}

	var resp ShipResponse
	err := s.repo.WithTx(ctx, func(repo shippingRepository) error {
		// 1. Блокируем и проверяем буфер отгрузки.
		bin, err := repo.GetBufferBinForUpdate(ctx, req.BufferBinID)
		if err != nil {
			return fmt.Errorf("shipping.Service.Ship get bin: %w", err)
		}
		if bin.Section == nil || *bin.Section != binSectionShippingBuffer || bin.DestinationID == nil {
			return fmt.Errorf("shipping.Service.Ship: %w", ErrBinNotShippingBuffer)
		}

		// 2. Блокируем и проверяем рейс: машина должна быть у ворот, destination должен совпадать.
		dispatch, err := repo.GetDispatchForUpdate(ctx, req.DispatchID)
		if err != nil {
			return fmt.Errorf("shipping.Service.Ship get dispatch: %w", err)
		}
		if *bin.DestinationID != dispatch.DestinationID {
			return fmt.Errorf("shipping.Service.Ship: %w", ErrDestinationMismatch)
		}
		if dispatch.Status != domain.OutboundDispatchStatusAtGate {
			return fmt.Errorf("shipping.Service.Ship: %w", ErrDispatchNotAtGate)
		}

		// 3. Блокируем товары для текущей отгрузки в режиме bulk или spot.
		products, err := repo.SelectProductsForShip(ctx, req.BufferBinID, req.ProductIDs)
		if err != nil {
			return fmt.Errorf("shipping.Service.Ship select products: %w", err)
		}
		if len(req.ProductIDs) == 0 && len(products) == 0 {
			return fmt.Errorf("shipping.Service.Ship: %w", ErrBufferEmpty)
		}
		if len(req.ProductIDs) > 0 && len(products) != len(req.ProductIDs) {
			return fmt.Errorf("shipping.Service.Ship: %w", ErrProductNotInBuffer)
		}

		productIDs := make([]uuid.UUID, 0, len(products))
		orderSet := make(map[uuid.UUID]struct{})
		events := make([]shippingEvent, 0, len(products))
		for _, p := range products {
			productIDs = append(productIDs, p.ProductID)
			events = append(events, shippingEvent{EventID: uuid.New(), ProductID: p.ProductID})
			if p.OrderID != nil {
				// orderSet хранит заказы, затронутые этой отгрузкой, и автоматически убирает дубли.
				orderSet[*p.OrderID] = struct{}{}
			}
		}

		// 4. Переводим товары в статус SHIPPED.
		if err := repo.BatchUpdateProductsShipped(ctx, productIDs); err != nil {
			return fmt.Errorf("shipping.Service.Ship update products: %w", err)
		}
		// 5. Создаём запись shippings для каждого товара.
		if err := repo.BatchInsertShippings(ctx, events, req.DispatchID, req.OperatorID); err != nil {
			return fmt.Errorf("shipping.Service.Ship insert shippings: %w", err)
		}
		// 6. Создаём shipping outbox event для каждого товара, чтобы запустить on-chain переход Picked -> Shipped.
		if err := repo.BatchInsertShippingOutbox(ctx, events, req.DispatchID); err != nil {
			return fmt.Errorf("shipping.Service.Ship insert outbox: %w", err)
		}

		// 7. Переводим заказ в SHIPPED, если все его товары уже отгружены.
		orderIDs := make([]uuid.UUID, 0, len(orderSet))
		for orderID := range orderSet {
			orderIDs = append(orderIDs, orderID)
		}
		ordersCompleted, err := repo.UpdateOrdersShippedConditional(ctx, orderIDs)
		if err != nil {
			return fmt.Errorf("shipping.Service.Ship update orders: %w", err)
		}

		// 8. Проверяем остаток в буфере; если буфер пуст, переводим рейс в DEPARTED.
		remaining, err := repo.CountReadyToShipProductsInBuffer(ctx, req.BufferBinID)
		if err != nil {
			return fmt.Errorf("shipping.Service.Ship count remaining: %w", err)
		}

		dispatchDeparted := false
		if remaining == 0 {
			if err := repo.UpdateDispatchDeparted(ctx, req.DispatchID); err != nil {
				return fmt.Errorf("shipping.Service.Ship depart dispatch: %w", err)
			}
			dispatchDeparted = true
		}

		resp = ShipResponse{
			ProductsShipped:  len(products),
			OrdersCompleted:  ordersCompleted,
			DispatchDeparted: dispatchDeparted,
			BufferRemaining:  remaining,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &resp, nil
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

func payloadHashForShipping(productID, dispatchID uuid.UUID) (string, error) {
	payload := struct {
		ProductID     uuid.UUID `json:"product_id"`
		DispatchID    uuid.UUID `json:"dispatch_id"`
		AggregateType string    `json:"aggregate_type"`
		EventType     string    `json:"event_type"`
	}{
		ProductID:     productID,
		DispatchID:    dispatchID,
		AggregateType: shippingAggregateType,
		EventType:     shippingEventType,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("shipping.payloadHashForShipping marshal: %w", err)
	}

	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
