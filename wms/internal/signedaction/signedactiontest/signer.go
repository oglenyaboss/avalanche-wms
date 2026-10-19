// Package signedactiontest provides a software P-256 signer that produces
// assertions byte-for-byte like the browser test vectors. Import in tests only.
package signedactiontest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"

	"wms/internal/signedaction"
)

// Origin is the browser origin the software signer asserts.
const Origin = "http://localhost:5173"

// RPID is the WebAuthn RP ID the software signer asserts.
const RPID = "localhost"

// KeyFromScalar builds a P-256 key from a big-endian scalar.
func KeyFromScalar(d []byte) *ecdsa.PrivateKey {
	k := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()}, D: new(big.Int).SetBytes(d)}
	k.X, k.Y = k.Curve.ScalarBaseMult(k.D.Bytes())
	return k
}

// NewKey generates a random P-256 key, panicking on the impossible error.
func NewKey() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}

// Coordinates returns the public key as the two ABI words qx and qy.
func Coordinates(k *ecdsa.PrivateKey) (qx, qy [32]byte) {
	k.X.FillBytes(qx[:])
	k.Y.FillBytes(qy[:])
	return qx, qy
}

// Sign produces a browser-shaped assertion over the challenge.
func Sign(k *ecdsa.PrivateKey, challenge [32]byte) signedaction.Assertion {
	cdj := `{"type":"webauthn.get","challenge":"` +
		base64.RawURLEncoding.EncodeToString(challenge[:]) +
		`","origin":"` + Origin + `","crossOrigin":false}`

	rpHash := sha256.Sum256([]byte(RPID))
	authData := append(rpHash[:], 0x05)
	authData = binary.BigEndian.AppendUint32(authData, 1)

	clientHash := sha256.Sum256([]byte(cdj))
	h := sha256.New()
	h.Write(authData)
	h.Write(clientHash[:])
	sig, err := ecdsa.SignASN1(rand.Reader, k, h.Sum(nil))
	if err != nil {
		panic(err)
	}
	return signedaction.Assertion{
		AuthenticatorData: authData,
		ClientDataJSON:    []byte(cdj),
		Signature:         sig,
	}
}
