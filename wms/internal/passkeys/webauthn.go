package passkeys

import (
	"fmt"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"golang.org/x/crypto/sha3"
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

// p256Coordinates extracts the X and Y coordinates of an ES256 / P-256 COSE key,
// right-aligned into 32-byte arrays.
func p256Coordinates(cosePublicKey []byte) (qx, qy [32]byte, err error) {
	parsed, err := webauthncose.ParsePublicKey(cosePublicKey)
	if err != nil {
		return qx, qy, fmt.Errorf("passkeys.p256Coordinates parse: %w", err)
	}
	key, ok := parsed.(webauthncose.EC2PublicKeyData)
	if !ok {
		return qx, qy, fmt.Errorf("passkeys.p256Coordinates: key is not EC2")
	}
	if key.Algorithm != int64(webauthncose.AlgES256) || key.Curve != p256CurveID {
		return qx, qy, fmt.Errorf("passkeys.p256Coordinates: unsupported alg %d or curve %d", key.Algorithm, key.Curve)
	}
	if len(key.XCoord) > 32 || len(key.YCoord) > 32 {
		return qx, qy, fmt.Errorf("passkeys.p256Coordinates: coordinate longer than 32 bytes")
	}
	copy(qx[32-len(key.XCoord):], key.XCoord)
	copy(qy[32-len(key.YCoord):], key.YCoord)
	return qx, qy, nil
}

// keyID is keccak256(qx || qy), the identifier of the key in the on-chain registry.
func keyID(qx, qy [32]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(qx[:])
	h.Write(qy[:])
	var out [32]byte
	h.Sum(out[:0])
	return out
}
