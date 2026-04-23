package domain

import "github.com/google/uuid"

type OrderLine struct {
	ID      int64     `json:"id"`
	OrderID uuid.UUID `json:"order_id"`
	SKUID   uuid.UUID `json:"sku_id"`
	Qty     int       `json:"qty"`
}
