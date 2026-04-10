package putaway

import (
	"time"

	"github.com/google/uuid"
)

type InsertPutawayParams struct {
	EventID       uuid.UUID
	ProductID     uuid.UUID
	BinID         uuid.UUID
	OperatorID    uuid.UUID
	OnChainStatus string
	OccurredAt    time.Time
}

type ProductBufferItem struct {
	ProductID uuid.UUID
	QRCode    string
	Status    string
	SKUName   string
}

type OutboxEventsParams struct {
	ProductIDs   []uuid.UUID
	StorageBinID uuid.UUID
}
