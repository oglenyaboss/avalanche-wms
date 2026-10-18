package domain

import (
	"time"

	"github.com/google/uuid"
)

type PasskeyStatus string

const (
	PasskeyStatusPending  PasskeyStatus = "PENDING"
	PasskeyStatusActive   PasskeyStatus = "ACTIVE"
	PasskeyStatusRejected PasskeyStatus = "REJECTED"
	PasskeyStatusRevoked  PasskeyStatus = "REVOKED"
)

type Passkey struct {
	CredentialID []byte
	UserID       uuid.UUID
	OrgID        uuid.UUID
	QX           [32]byte
	QY           [32]byte
	KeyID        [32]byte
	Role         UserRole
	Status       PasskeyStatus
	CreatedAt    time.Time
}
