package domain

import (
	"time"

	"github.com/google/uuid"
)

type Bin struct {
	BinID       uuid.UUID `json:"bin_id"`
	WarehouseID int64     `json:"warehouse_id"`
	Code        string    `json:"code"`
	Section     *string   `json:"section"`
	Volume      *float64  `json:"volume"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
