package domain

import (
	"time"

	"github.com/google/uuid"
)

type Order struct {
	OrderID         uuid.UUID   `json:"order_id"`
	ExternalOrderNo string      `json:"external_order_no"`
	CustomerID      uuid.UUID   `json:"customer_id"`
	WarehouseID     int64       `json:"warehouse_id"`
	Status          OrderStatus `json:"status"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}
