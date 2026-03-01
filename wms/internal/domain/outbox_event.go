package domain

import (
	"time"

	"github.com/google/uuid"
)

type OutboxEvent struct {
	ID            int64     `json:"id"`
	EventID       uuid.UUID `json:"event_id"`
	AggregateID   uuid.UUID `json:"aggregate_id"`
	AggregateType string    `json:"aggregate_type"`
	EventType     string    `json:"event_type"`
	PayloadHash   string    `json:"payload_hash"`
	CreatedAt     time.Time `json:"created_at"`
}
