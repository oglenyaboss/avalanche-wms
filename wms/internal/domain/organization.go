package domain

import "github.com/google/uuid"

type Organization struct {
	ID   uuid.UUID
	Name string
}
