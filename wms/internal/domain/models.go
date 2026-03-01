package domain

import "time"

type Warehouse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type CargoPlace struct {
	ID        int64     `json:"id"`
	Barcode   string    `json:"barcode"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
