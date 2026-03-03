package domain

import (
	"time"

	"github.com/google/uuid"
)

type SKU struct {
	SKUID       uuid.UUID `json:"sku_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Volume      *float64  `json:"volume"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
