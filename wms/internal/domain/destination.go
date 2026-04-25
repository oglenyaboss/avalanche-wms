package domain

import (
	"time"

	"github.com/google/uuid"
)

// Destination описывает магазин-получатель для заказов, буферов отгрузки и исходящих рейсов.
type Destination struct {
	DestinationID uuid.UUID `json:"destination_id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Address       *string   `json:"address"`
	WarehouseID   int64     `json:"warehouse_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
