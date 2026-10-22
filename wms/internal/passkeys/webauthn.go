package passkeys

import (
	"fmt"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"wms/internal/domain"
)

// webauthnUser adapts a WMS user to the library's webauthn.User. The user handle
// is the WMS user UUID; the library never sees existing credentials because a
// user may hold at most one live passkey, which is checked before begin.
type webauthnUser struct {
	id       uuid.UUID
	username string
}

func (u webauthnUser) WebAuthnID() []byte                         { return u.id[:] }
func (u webauthnUser) WebAuthnName() string                       { return u.username }
func (u webauthnUser) WebAuthnDisplayName() string                { return u.username }
func (u webauthnUser) WebAuthnCredentials() []webauthn.Credential { return nil }

const p256CurveID = 1

// p256Coordinates extracts the P-256 public key of an ES256 COSE key,
// its coordinates right-aligned into 32-byte arrays.
func p256Coordinates(cosePublicKey []byte) (domain.P256Point, error) {
	var out domain.P256Point
	parsed, err := webauthncose.ParsePublicKey(cosePublicKey)
	if err != nil {
		return out, fmt.Errorf("passkeys.p256Coordinates parse: %w", err)
	}
	key, ok := parsed.(webauthncose.EC2PublicKeyData)
	if !ok {
		return out, fmt.Errorf("passkeys.p256Coordinates: key is not EC2")
	}
	if key.Algorithm != int64(webauthncose.AlgES256) || key.Curve != p256CurveID {
		return out, fmt.Errorf("passkeys.p256Coordinates: unsupported alg %d or curve %d", key.Algorithm, key.Curve)
	}
	if len(key.XCoord) > 32 || len(key.YCoord) > 32 {
		return out, fmt.Errorf("passkeys.p256Coordinates: coordinate longer than 32 bytes")
	}
	copy(out.X[32-len(key.XCoord):], key.XCoord)
	copy(out.Y[32-len(key.YCoord):], key.YCoord)
	return out, nil
}
