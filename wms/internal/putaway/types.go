package putaway

import (
	"time"

	"github.com/google/uuid"
)

type InsertPutawayParams struct {
	EventID    uuid.UUID
	ProductID  uuid.UUID
	FromBinID  uuid.UUID
	BinID      uuid.UUID
	OperatorID uuid.UUID
	OccurredAt time.Time
	// ActionID — подписанное действие (часть операции), которое разместило товар (ADR-0001 §4).
	ActionID uuid.UUID
}

type ProductBufferItem struct {
	ProductID uuid.UUID
	QRCode    string
	Status    string
	SKUName   string
}
