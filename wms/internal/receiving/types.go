package receiving

import (
	"time"

	"github.com/google/uuid"

	"wms/internal/domain"
)

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

type GateLogParams struct {
	TTNCode        *string
	CargoplaceCode *string
	ShipmentID     *uuid.UUID
	CargoplaceID   *uuid.UUID
	OperatorID     uuid.UUID
	Action         string
	OccurredAt     time.Time
}

type TableLogParams struct {
	CargoplaceID uuid.UUID
	BoxID        *uuid.UUID
	OperatorID   uuid.UUID
	Action       string
	BoxBarcode   *string
	SKUID        *uuid.UUID
	QRCode       *string
	ProductID    *uuid.UUID
	BufferBinID  *uuid.UUID
	OccurredAt   time.Time
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
