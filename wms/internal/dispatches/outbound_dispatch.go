package dispatches

import (
	"time"

	"github.com/google/uuid"
)

/*
CREATE TABLE wms_inventory.outbound_dispatches (
  dispatch_id uuid PRIMARY KEY,
  dispatch_code text NOT NULL,
  warehouse_id bigint NOT NULL REFERENCES wms_inventory.warehouses(warehouse_id) ON DELETE RESTRICT,
  destination_id uuid NOT NULL REFERENCES wms_inventory.destinations(destination_id) ON DELETE RESTRICT,
  vehicle_number text NOT NULL,
  driver_name text NOT NULL,
  driver_phone text,
  status wms_inventory.outbound_dispatch_status NOT NULL DEFAULT 'SCHEDULED',
  scheduled_at timestamptz NOT NULL,
  arrived_at timestamptz,
  departed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT outbound_dispatches_dispatch_code_unique UNIQUE (dispatch_code)
);
*/

type OutboundDispatch struct {
	DispatchId    uuid.UUID              `json:"dispatch_id"`
	DispatchCode  string                 `json:"dispatch_code"`
	WarehouseId   int64                  `json:"warehouse_id"`
	DestinationId uuid.UUID              `json:"destination_id"`
	VehicleNumber string                 `json:"vehicle_number"`
	DriverName    string                 `json:"driver_name"`
	DriverPhone   *string                `json:"driver_phone,omitempty"`
	Status        OutboundDispatchStatus `json:"status"`
	ScheduledAt   time.Time              `json:"scheduled_at"`
	ArrivedAt     *time.Time             `json:"arrived_at,omitempty"`
	DepartedAt    *time.Time             `json:"departed_at,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}
