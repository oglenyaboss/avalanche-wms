package signedaction

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// ConfirmationState is the derived chain confirmation of a submitted signed action.
type ConfirmationState string

const (
	ConfirmationSent     ConfirmationState = "SENT"
	ConfirmationApplied  ConfirmationState = "APPLIED"
	ConfirmationRejected ConfirmationState = "REJECTED"
	ConfirmationFailed   ConfirmationState = "FAILED"
)

// ConfirmationStateOf derives the confirmation state from the onchain_events
// status and reject_reason columns: NULL status means the event is still in
// the outbox, COMMITTED means applied, FAILED with a reason means rejected.
func ConfirmationStateOf(status *string, reason *int16) (ConfirmationState, *int16) {
	if status == nil {
		return ConfirmationSent, nil
	}
	switch *status {
	case "COMMITTED":
		return ConfirmationApplied, nil
	case "FAILED":
		if reason != nil {
			return ConfirmationRejected, reason
		}
		return ConfirmationFailed, nil
	default: // PENDING, SENT
		return ConfirmationSent, nil
	}
}

// Hex32 renders a 32-byte value as "0x" + 64 lowercase hex characters.
func Hex32(v [32]byte) string {
	return "0x" + hex.EncodeToString(v[:])
}

// EnvelopeWebAuthn is the webauthn block of the signed-action envelope (ADR §9).
type EnvelopeWebAuthn struct {
	AuthenticatorData string `json:"authenticatorData"`
	ClientDataJSON    string `json:"clientDataJSON"`
	ChallengeIndex    int    `json:"challengeIndex"`
	TypeIndex         int    `json:"typeIndex"`
	R                 string `json:"r"`
	S                 string `json:"s"`
}

// NewEnvelopeWebAuthn renders a verified WebAuthn result for the envelope.
func NewEnvelopeWebAuthn(wa WebAuthn) EnvelopeWebAuthn {
	return EnvelopeWebAuthn{
		AuthenticatorData: "0x" + hex.EncodeToString(wa.AuthenticatorData),
		ClientDataJSON:    wa.ClientDataJSON,
		ChallengeIndex:    wa.ChallengeIndex,
		TypeIndex:         wa.TypeIndex,
		R:                 "0x" + hex.EncodeToString(wa.R[:]),
		S:                 "0x" + hex.EncodeToString(wa.S[:]),
	}
}

// ErrInvalidRequest is returned when request body fields do not decode.
var ErrInvalidRequest = errors.New("invalid request")

// AssertionRequest is the submit body for one WebAuthn assertion, all fields
// base64url without padding as navigator.credentials.get returns them.
type AssertionRequest struct {
	CredentialID      string `json:"credential_id"`
	AuthenticatorData string `json:"authenticator_data"`
	ClientDataJSON    string `json:"client_data_json"`
	Signature         string `json:"signature"`
}

// Decode decodes the four base64url body fields into an Assertion.
func (r AssertionRequest) Decode() (Assertion, error) {
	decode := base64.RawURLEncoding.DecodeString
	credID, err := decode(r.CredentialID)
	if err != nil {
		return Assertion{}, err
	}
	authData, err := decode(r.AuthenticatorData)
	if err != nil {
		return Assertion{}, err
	}
	cdj, err := decode(r.ClientDataJSON)
	if err != nil {
		return Assertion{}, err
	}
	sig, err := decode(r.Signature)
	if err != nil {
		return Assertion{}, err
	}
	return Assertion{
		CredentialID:      credID,
		AuthenticatorData: authData,
		ClientDataJSON:    cdj,
		Signature:         sig,
	}, nil
}
