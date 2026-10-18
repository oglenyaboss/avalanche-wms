package signedaction_test

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
)

type hexBytes []byte

func (h *hexBytes) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return err
	}
	*h = v
	return nil
}

type vectorAction struct {
	ActionID        string   `json:"actionId"`
	ActionIDBytes16 hexBytes `json:"actionIdBytes16"`
	ActionType      int      `json:"actionType"`
	Challenge       hexBytes `json:"challenge"`
	Details         struct {
		QX hexBytes `json:"qx"`
		QY hexBytes `json:"qy"`
	} `json:"details"`
	DetailsHash hexBytes `json:"detailsHash"`
	ItemsHash   hexBytes `json:"itemsHash"`
	KeyID       hexBytes `json:"keyId"`
	Name        string   `json:"name"`
	WebAuthn    struct {
		AuthenticatorData hexBytes `json:"authenticatorData"`
		ChallengeIndex    int      `json:"challengeIndex"`
		ClientDataJSONHex hexBytes `json:"clientDataJSONHex"`
		R                 hexBytes `json:"r"`
		S                 hexBytes `json:"s"`
		TypeIndex         int      `json:"typeIndex"`
	} `json:"webauthn"`
}

type vectorsFile struct {
	TypeHash    hexBytes `json:"typehash"`
	NoItemsHash hexBytes `json:"noItemsHash"`
	Signer      struct {
		KeyID      hexBytes `json:"keyId"`
		PrivateKey hexBytes `json:"privateKey"`
		QX         hexBytes `json:"qx"`
		QY         hexBytes `json:"qy"`
	} `json:"signer"`
	Actions []vectorAction `json:"actions"`
}

func loadVectors(t *testing.T) vectorsFile {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/signed-actions/vectors.v1.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectorsFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	return v
}

func TestVectors_Header(t *testing.T) {
	v := loadVectors(t)
	if signedaction.TypeHash != [32]byte(v.TypeHash) {
		t.Errorf("TypeHash = %x, want %x", signedaction.TypeHash, v.TypeHash)
	}
	if signedaction.NoItems != [32]byte(v.NoItemsHash) {
		t.Errorf("NoItems = %x, want %x", signedaction.NoItems, v.NoItemsHash)
	}
	if got := signedaction.KeyID([32]byte(v.Signer.QX), [32]byte(v.Signer.QY)); got != [32]byte(v.Signer.KeyID) {
		t.Errorf("KeyID(signer) = %x, want %x", got, v.Signer.KeyID)
	}
}

func TestVectors_Challenge(t *testing.T) {
	v := loadVectors(t)
	for _, a := range v.Actions {
		id, err := uuid.Parse(a.ActionID)
		if err != nil {
			t.Fatalf("%s: parse actionId: %v", a.Name, err)
		}
		got := signedaction.Challenge(id, uint8(a.ActionType), [32]byte(a.ItemsHash), [32]byte(a.DetailsHash))
		if got != [32]byte(a.Challenge) {
			t.Errorf("%s: challenge = %x, want %x", a.Name, got, a.Challenge)
		}
		if a.ActionType == int(signedaction.TypeKeyRegister) || a.ActionType == int(signedaction.TypeKeyRevoke) {
			gotDetails := signedaction.KeyDetailsHash([32]byte(a.Details.QX), [32]byte(a.Details.QY))
			if gotDetails != [32]byte(a.DetailsHash) {
				t.Errorf("%s: detailsHash = %x, want %x", a.Name, gotDetails, a.DetailsHash)
			}
		}
	}
}

func TestVectors_VerifyVectorSignature(t *testing.T) {
	v := loadVectors(t)
	for _, a := range v.Actions {
		der, err := asn1.Marshal(struct{ R, S *big.Int }{
			new(big.Int).SetBytes(a.WebAuthn.R),
			new(big.Int).SetBytes(a.WebAuthn.S),
		})
		if err != nil {
			t.Fatalf("%s: encode DER: %v", a.Name, err)
		}
		got, err := signedaction.Verify(signedaction.Assertion{
			AuthenticatorData: a.WebAuthn.AuthenticatorData,
			ClientDataJSON:    a.WebAuthn.ClientDataJSONHex,
			Signature:         der,
		}, [32]byte(a.Challenge), [32]byte(v.Signer.QX), [32]byte(v.Signer.QY), signedactiontest.RPID, []string{signedactiontest.Origin})
		if err != nil {
			t.Fatalf("%s: Verify: %v", a.Name, err)
		}
		if got.ChallengeIndex != a.WebAuthn.ChallengeIndex || got.TypeIndex != a.WebAuthn.TypeIndex {
			t.Errorf("%s: indexes = (%d, %d), want (%d, %d)", a.Name, got.ChallengeIndex, got.TypeIndex, a.WebAuthn.ChallengeIndex, a.WebAuthn.TypeIndex)
		}
		if got.R != [32]byte(a.WebAuthn.R) || got.S != [32]byte(a.WebAuthn.S) {
			t.Errorf("%s: r/s = %x/%x, want %x/%x", a.Name, got.R, got.S, a.WebAuthn.R, a.WebAuthn.S)
		}
	}
}

func TestVectors_SoftwareSignerMatchesBrowserFormat(t *testing.T) {
	v := loadVectors(t)
	key := signedactiontest.KeyFromScalar(v.Signer.PrivateKey)
	qx, qy := signedactiontest.Coordinates(key)
	if qx != [32]byte(v.Signer.QX) || qy != [32]byte(v.Signer.QY) {
		t.Fatalf("software signer key = %x/%x, want %x/%x", qx, qy, v.Signer.QX, v.Signer.QY)
	}
	for _, a := range v.Actions {
		assertion := signedactiontest.Sign(key, [32]byte(a.Challenge))
		if !bytes.Equal(assertion.ClientDataJSON, a.WebAuthn.ClientDataJSONHex) {
			t.Errorf("%s: clientDataJSON = %s, want %s", a.Name, assertion.ClientDataJSON, a.WebAuthn.ClientDataJSONHex)
		}
		if !bytes.Equal(assertion.AuthenticatorData, a.WebAuthn.AuthenticatorData) {
			t.Errorf("%s: authenticatorData = %x, want %x", a.Name, assertion.AuthenticatorData, a.WebAuthn.AuthenticatorData)
		}
		if _, err := signedaction.Verify(assertion, [32]byte(a.Challenge), qx, qy, signedactiontest.RPID, []string{signedactiontest.Origin}); err != nil {
			t.Errorf("%s: Verify: %v", a.Name, err)
		}
	}
}
