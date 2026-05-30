package shipping

import (
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

type scanBufferRequest struct {
	BufferBinID string `json:"buffer_bin_id"`
}

type scanDriverRequest struct {
	DispatchCode string `json:"dispatch_code"`
}

type shipHTTPRequest struct {
	BufferBinID string   `json:"buffer_bin_id"`
	DispatchID  string   `json:"dispatch_id"`
	ProductIDs  []string `json:"product_ids"`
}

type ShipRequest struct {
	BufferBinID uuid.UUID
	DispatchID  uuid.UUID
	OperatorID  uuid.UUID
	ProductIDs  []uuid.UUID
}

type ShipResponse struct {
	// ProductsShipped показывает количество товаров, фактически отгруженных в этом запросе.
	ProductsShipped int `json:"products_shipped"`
	// OutboxEventsCreated показывает количество событий, созданных для on-chain перехода Picked -> Shipped.
	OutboxEventsCreated int `json:"outbox_events_created"`
	// OrdersCompleted показывает количество заказов, переведённых в статус SHIPPED после отгрузки.
	OrdersCompleted int `json:"orders_completed"`
	// OrdersPartiallyShipped показывает количество заказов, переведённых в PARTIALLY_SHIPPED
	// (часть товаров отгружена, часть ещё в буфере) в этом запросе.
	OrdersPartiallyShipped int `json:"orders_partially_shipped"`
	// DispatchDeparted показывает, был ли буфер очищен и переведён ли рейс в DEPARTED.
	DispatchDeparted bool `json:"dispatch_departed"`
	// BufferRemaining показывает количество товаров READY_TO_SHIP, оставшихся в буфере после отгрузки.
	BufferRemaining int `json:"buffer_remaining"`
}

type DestinationResponse struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

type BufferBinResponse struct {
	ID          uuid.UUID           `json:"id"`
	Code        string              `json:"code"`
	Destination DestinationResponse `json:"destination"`
}

type ScanBufferProductResponse struct {
	ProductID       uuid.UUID `json:"product_id"`
	QRCode          string    `json:"qr_code"`
	SKUName         string    `json:"sku_name"`
	OrderExternalNo *string   `json:"order_external_no"`
}

type ScanBufferResponse struct {
	BufferBin BufferBinResponse           `json:"buffer_bin"`
	Products  []ScanBufferProductResponse `json:"products"`
	Count     int                         `json:"count"`
}

type ScanDriverResponse struct {
	DispatchID    uuid.UUID                     `json:"dispatch_id"`
	DispatchCode  string                        `json:"dispatch_code"`
	VehicleNumber string                        `json:"vehicle_number"`
	DriverName    string                        `json:"driver_name"`
	DriverPhone   *string                       `json:"driver_phone"`
	Destination   DestinationResponse           `json:"destination"`
	Status        domain.OutboundDispatchStatus `json:"status"`
	ArrivedAt     *time.Time                    `json:"arrived_at"`
}

type BufferBinRecord struct {
	BinID           uuid.UUID
	Code            string
	Section         *string
	DestinationID   *uuid.UUID
	DestinationCode *string
	DestinationName *string
}

type ReadyToShipProduct struct {
	ProductID       uuid.UUID
	QRCode          string
	SKUName         string
	OrderExternalNo *string
}

type ProductForShip struct {
	ProductID uuid.UUID
	OrderID   *uuid.UUID
}

type shippingEvent struct {
	EventID   uuid.UUID
	ProductID uuid.UUID
}

type DispatchRecord struct {
	DispatchID      uuid.UUID
	DispatchCode    string
	DestinationID   uuid.UUID
	DestinationCode string
	DestinationName string
	VehicleNumber   string
	DriverName      string
	DriverPhone     *string
	Status          domain.OutboundDispatchStatus
	ArrivedAt       *time.Time
}
