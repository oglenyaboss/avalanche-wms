package domain

import (
	"time"

	"github.com/google/uuid"
)

type Shipping struct {
	ID         int64     `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	ProductID  uuid.UUID `json:"product_id"`
	DispatchID uuid.UUID `json:"dispatch_id"`
	OperatorID uuid.UUID `json:"operator_id"`
	ShippedAt  time.Time `json:"shipped_at"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
}
