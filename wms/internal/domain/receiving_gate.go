package domain

import (
	"time"

	"github.com/google/uuid"
)

type ReceivingGateEntry struct {
	ID             int64      `json:"id"`
	TTNCode        *string    `json:"ttn_code"`
	CargoplaceCode *string    `json:"cargoplace_code"`
	EventID        uuid.UUID  `json:"event_id"`
	ShipmentID     *uuid.UUID `json:"shipment_id"`
	CargoplaceID   *uuid.UUID `json:"cargoplace_id"`
	OperatorID     uuid.UUID  `json:"operator_id"`
	Action         string     `json:"action"`
	OccurredAt     time.Time  `json:"occurred_at"`
	CreatedAt      time.Time  `json:"created_at"`
}
