package domain

import (
	"time"

	"github.com/google/uuid"
)

type Cargoplace struct {
	CargoplaceID     uuid.UUID  `json:"cargoplace_id"`
	ShipmentID       uuid.UUID  `json:"shipment_id"`
	CargoplaceCode   string     `json:"cargoplace_code"`
	Status           string     `json:"status"`
	ReceivedAtGateAt *time.Time `json:"received_at_gate_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
