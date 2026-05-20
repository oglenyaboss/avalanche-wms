package domain

import (
	"time"

	"github.com/google/uuid"
)

type Putaway struct {
	ID         int64     `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	ProductID  uuid.UUID `json:"product_id"`
	FromBinID  uuid.UUID `json:"from_bin_id"`
	BinID      uuid.UUID `json:"bin_id"`
	OperatorID uuid.UUID `json:"operator_id"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
}
