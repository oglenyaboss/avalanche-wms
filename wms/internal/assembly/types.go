package assembly

import (
	"time"

	"github.com/google/uuid"
)

type AllocateRequest struct {
	DestinationID string `json:"destination_id"`
}

type AllocateResponse struct {
	AllocatedOrders    int                 `json:"allocated_orders"`
	AllocatedProducts  int                 `json:"allocated_products"`
	InsufficientOrders []InsufficientOrder `json:"insufficient_orders"`
}

type InsufficientOrder struct {
	OrderID     string            `json:"order_id"`
	MissingSKUs []InsufficientSKU `json:"missing_skus"`
}

type InsufficientSKU struct {
	SKUID      string `json:"sku_id"`
	SKUName    string `json:"sku_name"`
	MissingQty int    `json:"missing_qty"`
}

type TaskResponse struct {
	Tasks []TaskItem `json:"tasks"`
}

type TaskItem struct {
	TaskID         string `json:"task_id"`
	ProductID      string `json:"product_id"`
	QRCode         string `json:"qr_code"`
	SKUName        string `json:"sku_name"`
	FromBinCode    string `json:"from_bin_code"`
	FromBinSection string `json:"from_bin_section"`
	OrderNo        string `json:"order_no"`
}

type PickRequest struct {
	ProductID string `json:"product_id"`
}

type PickResponse struct {
	ProductID string `json:"product_id"`
	CartSize  int    `json:"cart_size"`
}

type AllocatedProduct struct {
	ProductID     uuid.UUID
	OrderID       uuid.UUID
	BinID         uuid.UUID
	SKUID         uuid.UUID
	DestinationID uuid.UUID
}

type Task struct {
	EventID       uuid.UUID
	OrderID       uuid.UUID
	ProductID     uuid.UUID
	SKUID         uuid.UUID
	FromBinID     uuid.UUID
	Section       string
	Status        string
	OccurredAt    time.Time
	DestinationID uuid.UUID
}
