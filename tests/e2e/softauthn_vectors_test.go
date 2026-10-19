// Vector test for the software signer: proves the e2e challenge and assertion
// construction is byte-for-byte identical to the contract test vectors. No build
// tag, so it runs without the Docker stack.

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type softVectorAction struct {
	ActionID        string `json:"actionId"`
	ActionIDBytes16 string `json:"actionIdBytes16"`
	ActionType      int    `json:"actionType"`
	Challenge       string `json:"challenge"`
	Details         struct {
		QX string `json:"qx"`
		QY string `json:"qy"`
	} `json:"details"`
	DetailsHash string `json:"detailsHash"`
	ItemsHash   string `json:"itemsHash"`
	KeyID       string `json:"keyId"`
	Name        string `json:"name"`
	WebAuthn    struct {
		AuthenticatorData string `json:"authenticatorData"`
		ChallengeIndex    int    `json:"challengeIndex"`
		ClientDataJSONHex string `json:"clientDataJSONHex"`
		R                 string `json:"r"`
		S                 string `json:"s"`
		TypeIndex         int    `json:"typeIndex"`
	} `json:"webauthn"`
}

type softVectorsFile struct {
	TypeHash    string `json:"typehash"`
	NoItemsHash string `json:"noItemsHash"`
	Signer      struct {
		KeyID      string `json:"keyId"`
		PrivateKey string `json:"privateKey"`
		QX         string `json:"qx"`
		QY         string `json:"qy"`
	} `json:"signer"`
	Actions []softVectorAction `json:"actions"`
}

func loadSoftVectors(t *testing.T) softVectorsFile {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/signed-actions/vectors.v1.json")
	require.NoError(t, err)
	var v softVectorsFile
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

// softHex decodes a 0x-prefixed hex string into bytes.
func softHex(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		panic(err)
	}
	return b
}

func softHex32(s string) [32]byte {
	var out [32]byte
	copy(out[:], softHex(s))
	return out
}

func TestSoftAuthn_Vectors(t *testing.T) {
	v := loadSoftVectors(t)

	require.Equal(t, softTypeHash, softHex(v.TypeHash), "typehash mismatch")
	require.Equal(t, softNoItems, softHex(v.NoItemsHash), "noItemsHash mismatch")

	signerKey := softKeyFromScalar(softHex(v.Signer.PrivateKey))
	qx, qy := softCoordinates(signerKey)
	require.Equal(t, softHex(v.Signer.QX), qx[:], "signer qx mismatch")
	require.Equal(t, softHex(v.Signer.QY), qy[:], "signer qy mismatch")
	keyID := softKeyID(qx, qy)
	require.Equal(t, softHex(v.Signer.KeyID), keyID[:], "signer keyId mismatch")

	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(qx[:]), Y: new(big.Int).SetBytes(qy[:])}

	for _, a := range v.Actions {
		t.Run(a.Name, func(t *testing.T) {
			id, err := uuid.Parse(a.ActionID)
			require.NoError(t, err)

			gotChallenge := softChallenge(id, uint8(a.ActionType), softHex32(a.ItemsHash), softHex32(a.DetailsHash))
			require.Equal(t, softHex(a.Challenge), gotChallenge[:], "challenge mismatch")

			assertion := softSign(signerKey, gotChallenge)
			require.Equal(t, softHex(a.WebAuthn.ClientDataJSONHex), assertion.ClientDataJSON, "clientDataJSON mismatch")
			require.Equal(t, softHex(a.WebAuthn.AuthenticatorData), assertion.AuthenticatorData, "authenticatorData mismatch")

			digest := assertionDigest(t, assertion.AuthenticatorData, assertion.ClientDataJSON)
			require.True(t, ecdsa.VerifyASN1(pub, digest, assertion.Signature), "softSign signature does not verify")

			vectorSig, err := asn1.Marshal(struct{ R, S *big.Int }{
				new(big.Int).SetBytes(softHex(a.WebAuthn.R)),
				new(big.Int).SetBytes(softHex(a.WebAuthn.S)),
			})
			require.NoError(t, err)
			require.True(t, ecdsa.VerifyASN1(pub, digest, vectorSig), "vector signature does not verify")
		})
	}
}

// assertionDigest is sha256(authenticatorData || sha256(clientDataJSON)), the
// digest the WebAuthn signature covers.
func assertionDigest(t *testing.T, authData, cdj []byte) []byte {
	t.Helper()
	clientHash := sha256.Sum256(cdj)
	h := sha256.New()
	h.Write(authData)
	h.Write(clientHash[:])
	return h.Sum(nil)
}
