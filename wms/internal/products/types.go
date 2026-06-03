// Package products exposes read-only product lookups: a recent-activity list
// and a full lifecycle timeline (receiving→putaway→picking→shipping) with the
// on-chain status of each step.
package products

// RecentProduct is one row of the recent-activity list.
type RecentProduct struct {
	ProductID string `json:"product_id"`
	QRCode    string `json:"qr_code"`
	SKUName   string `json:"sku_name"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

// ProductHeader identifies the traced product.
type ProductHeader struct {
	ProductID string `json:"product_id"`
	QRCode    string `json:"qr_code"`
	SKUName   string `json:"sku_name"`
	Status    string `json:"status"`
}

// TimelineStep is one warehouse event with its on-chain status. Stage is the raw
// lowercase aggregate_type (receiving/putaway/picking/shipping); the frontend maps
// it to a label.
type TimelineStep struct {
	Stage          string  `json:"stage"`
	EventType      string  `json:"event_type"`
	OccurredAt     string  `json:"occurred_at"`
	EventID        string  `json:"event_id"`
	TxHash         *string `json:"tx_hash"`
	ChainStatus    string  `json:"chain_status"` // PENDING|SENT|COMMITTED|FAILED
	ChainUpdatedAt *string `json:"chain_updated_at"`
	ErrorMessage   *string `json:"error_message"`
}

// Timeline is the timeline endpoint payload. Steps may be empty (product exists
// but has no events yet) — that is a 200, not a 404.
type Timeline struct {
	Product ProductHeader  `json:"product"`
	Steps   []TimelineStep `json:"steps"`
}
