package domain

import (
	"time"

	"github.com/google/uuid"
)

// OutboundDispatch описывает плановый исходящий рейс, созданный внешней логистикой до погрузки.
type OutboundDispatch struct {
	DispatchID    uuid.UUID              `json:"dispatch_id"`
	DispatchCode  string                 `json:"dispatch_code"`
	WarehouseID   int64                  `json:"warehouse_id"`
	DestinationID uuid.UUID              `json:"destination_id"`
	VehicleNumber string                 `json:"vehicle_number"`
	DriverName    string                 `json:"driver_name"`
	DriverPhone   *string                `json:"driver_phone"`
	Status        OutboundDispatchStatus `json:"status"`
	ScheduledAt   time.Time              `json:"scheduled_at"`
	ArrivedAt     *time.Time             `json:"arrived_at"`
	DepartedAt    *time.Time             `json:"departed_at"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}
