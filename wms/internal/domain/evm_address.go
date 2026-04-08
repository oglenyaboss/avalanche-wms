package domain

import (
	"time"

	"github.com/google/uuid"
)

type EvmAddress struct {
	ID          int64     `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	EvmAddress  string    `json:"evm_address"`
	OnchainRole string    `json:"onchain_role"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
