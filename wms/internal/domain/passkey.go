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

// P256Point is an uncompressed P-256 public key (big-endian coordinates).
type P256Point struct {
	X [32]byte
	Y [32]byte
}

type Passkey struct {
	CredentialID []byte
	UserID       uuid.UUID
	OrgID        uuid.UUID
	Key          P256Point
	KeyID        [32]byte
	Role         UserRole
	Status       PasskeyStatus
	CreatedAt    time.Time
}
