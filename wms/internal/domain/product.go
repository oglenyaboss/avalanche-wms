package domain

import (
	"time"

	"github.com/google/uuid"
)

type Product struct {
	ProductID    uuid.UUID     `json:"product_id"`
	SKUID        uuid.UUID     `json:"sku_id"`
	ShipmentID   uuid.UUID     `json:"shipment_id"`
	CargoplaceID uuid.UUID     `json:"cargoplace_id"`
	BoxID        *uuid.UUID    `json:"box_id"`
	QRCode       string        `json:"qr_code"`
	BinID        *uuid.UUID    `json:"bin_id"`
	OrderID      *uuid.UUID    `json:"order_id"`
	Status       ProductStatus `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}
