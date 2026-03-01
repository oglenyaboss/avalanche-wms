package domain

import "github.com/google/uuid"

type ExpectedCargoplaceSku struct {
	ID           int64     `json:"id"`
	CargoplaceID uuid.UUID `json:"cargoplace_id"`
	SKUID        uuid.UUID `json:"sku_id"`
	ExpectedQty  int32     `json:"expected_qty"`
}
