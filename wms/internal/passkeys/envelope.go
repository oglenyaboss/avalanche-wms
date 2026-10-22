package passkeys

import (
	"wms/internal/domain"
	"wms/internal/signedaction"
)

// keyActionEnvelope matches the exact JSON keys of the signed-action envelope
// (contract §2) for a key action: no itemIds, no detailsHash.
type keyActionEnvelopeDoc struct {
	Version    int                           `json:"version"`
	ActionID   string                        `json:"actionId"`
	ActionType uint8                         `json:"actionType"`
	ItemIDs    []string                      `json:"itemIds"`
	SubjectKey subjectKeyJSON                `json:"subjectKey"`
	KeyID      string                        `json:"keyId"`
	WebAuthn   signedaction.EnvelopeWebAuthn `json:"webauthn"`
}

type subjectKeyJSON struct {
	QX string `json:"qx"`
	QY string `json:"qy"`
}

func keyActionEnvelope(action *KeyAction, target *domain.Passkey, signerKeyID [32]byte, wa signedaction.WebAuthn) keyActionEnvelopeDoc {
	return keyActionEnvelopeDoc{
		Version:    1,
		ActionID:   action.ActionID.String(),
		ActionType: action.ActionType,
		ItemIDs:    []string{},
		SubjectKey: subjectKeyJSON{QX: hexCoordinate(target.Key.X), QY: hexCoordinate(target.Key.Y)},
		KeyID:      hexCoordinate(signerKeyID),
		WebAuthn:   signedaction.NewEnvelopeWebAuthn(wa),
	}
}

func hexCoordinate(v [32]byte) string {
	return signedaction.Hex32(v)
}
