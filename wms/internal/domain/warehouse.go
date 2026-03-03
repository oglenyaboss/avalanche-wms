package domain

import "time"

type Warehouse struct {
	WarehouseID int64     `json:"warehouse_id"`
	Name        string    `json:"name"`
	Address     *string   `json:"address"`
	Contact     *string   `json:"contact"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
