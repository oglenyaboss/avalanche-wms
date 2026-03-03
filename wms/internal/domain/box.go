package domain

import (
	"time"

	"github.com/google/uuid"
)

type Box struct {
	BoxID        uuid.UUID `json:"box_id"`
	CargoplaceID uuid.UUID `json:"cargoplace_id"`
	BoxBarcode   string    `json:"box_barcode"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
