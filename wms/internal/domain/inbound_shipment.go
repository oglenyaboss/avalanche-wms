package domain

import (
	"time"

	"github.com/google/uuid"
)

type InboundShipment struct {
	ShipmentID  uuid.UUID `json:"shipment_id"`
	WarehouseID int64     `json:"warehouse_id"`
	TTNCode     string    `json:"ttn_code"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
