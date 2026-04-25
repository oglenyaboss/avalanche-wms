package domain

import (
	"time"

	"github.com/google/uuid"
)

type AssemblyTask struct {
	ID            int64                   `json:"id"`
	EventID       uuid.UUID               `json:"event_id"`
	OrderID       uuid.UUID               `json:"order_id"`
	ProductID     uuid.UUID               `json:"product_id"`
	SKUID         uuid.UUID               `json:"sku_id"`
	FromBinID     uuid.UUID               `json:"from_bin_id"`
	DestinationID uuid.UUID               `json:"destination_id"`
	Section       *string                 `json:"section"`
	Status        TaskStatus              `json:"status"`
	OperatorID    *uuid.UUID              `json:"operator_id"`
	OnchainStatus *OperationOnchainStatus `json:"onchain_status"`
	OnchainTxHash *string                 `json:"onchain_tx_hash"`
	OccurredAt    *time.Time              `json:"occurred_at"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}
