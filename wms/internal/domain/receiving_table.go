package domain

import (
	"time"

	"github.com/google/uuid"
)

type ReceivingTableEntry struct {
	ID           int64      `json:"id"`
	EventID      uuid.UUID  `json:"event_id"`
	CargoplaceID uuid.UUID  `json:"cargoplace_id"`
	BoxID        *uuid.UUID `json:"box_id"`
	OperatorID   uuid.UUID  `json:"operator_id"`
	Action       string     `json:"action"`
	BoxBarcode   *string    `json:"box_barcode"`
	SKUID        *uuid.UUID `json:"sku_id"`
	QRCode       *string    `json:"qr_code"`
	ProductID    *uuid.UUID `json:"product_id"`
	BufferBinID  *uuid.UUID `json:"buffer_bin_id"`
	OccurredAt   time.Time  `json:"occurred_at"`
	CreatedAt    time.Time  `json:"created_at"`
}
