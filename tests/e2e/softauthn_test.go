// Software P-256 signer for the signed-action e2e tests: byte-for-byte the same
// clientDataJSON and authenticatorData as wms/internal/signedaction/signedactiontest,
// with keccak via go-ethereum. This file has no build tag, so the helpers (and the
// vector test) run without the Docker stack.

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

const softOrigin = "http://localhost:5173"
const softRPID = "localhost"

// softTypeHash is keccak256("avalanche-wms/SignedAction/v1").
var softTypeHash = crypto.Keccak256([]byte("avalanche-wms/SignedAction/v1"))

// softNoItems is keccak256(""), the itemsHash of key actions.
var softNoItems = crypto.Keccak256(nil)

// softAssertion is the browser assertion parts the WMS submit API takes.
type softAssertion struct {
	AuthenticatorData []byte
	ClientDataJSON    []byte
	Signature         []byte
}

// softKeyFromScalar builds a P-256 key from a big-endian scalar.
func softKeyFromScalar(d []byte) *ecdsa.PrivateKey {
	k := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()}, D: new(big.Int).SetBytes(d)}
	k.X, k.Y = k.Curve.ScalarBaseMult(k.D.Bytes())
	return k
}

// newSoftKey generates a random P-256 key, panicking on the impossible error.
func newSoftKey() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}

// softCoordinates returns the public key as the two ABI words qx and qy.
func softCoordinates(k *ecdsa.PrivateKey) (qx, qy [32]byte) {
	k.X.FillBytes(qx[:])
	k.Y.FillBytes(qy[:])
	return qx, qy
}

// softSign produces a browser-shaped assertion over the challenge.
func softSign(k *ecdsa.PrivateKey, challenge [32]byte) softAssertion {
	cdj := `{"type":"webauthn.get","challenge":"` +
		base64.RawURLEncoding.EncodeToString(challenge[:]) +
		`","origin":"` + softOrigin + `","crossOrigin":false}`

	rpHash := sha256.Sum256([]byte(softRPID))
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
	return softAssertion{
		AuthenticatorData: authData,
		ClientDataJSON:    []byte(cdj),
		Signature:         sig,
	}
}

// softChallenge is keccak256 of the five ABI words: typehash, bytes16 actionId,
// uint8 actionType, itemsHash, detailsHash (ADR-0001 §3).
func softChallenge(actionID uuid.UUID, actionType uint8, itemsHash, detailsHash [32]byte) [32]byte {
	var buf [160]byte
	copy(buf[0:32], softTypeHash)
	copy(buf[32:48], actionID[:])
	buf[64+31] = actionType
	copy(buf[96:128], itemsHash[:])
	copy(buf[128:160], detailsHash[:])
	var out [32]byte
	copy(out[:], crypto.Keccak256(buf[:]))
	return out
}

// softKeyID is keccak256(qx || qy), the registry key id.
func softKeyID(qx, qy [32]byte) [32]byte {
	var out [32]byte
	copy(out[:], crypto.Keccak256(append(append([]byte{}, qx[:]...), qy[:]...)))
	return out
}
