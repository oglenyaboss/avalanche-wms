package signedops_test

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"wms/internal/domain"
	"wms/internal/signedaction"
	"wms/internal/signedaction/signedactiontest"
	"wms/internal/signedops"
)

func testSignerWithKey() (*domain.Passkey, *ecdsa.PrivateKey) {
	key := signedactiontest.NewKey()
	qx, qy := signedactiontest.Coordinates(key)
	return &domain.Passkey{
		CredentialID: []byte("cred-signer"),
		QX:           qx,
		QY:           qy,
		KeyID:        signedaction.KeyID(qx, qy),
	}, key
}

func receiveDetailsHash(salt [32]byte, _ []uuid.UUID) [32]byte {
	return signedaction.ReceiveDetailsHash(salt,
		uuid.MustParse("01929c5e-6f00-7000-8000-0000000000c1"),
		uuid.MustParse("01929c5e-6f00-7000-8000-0000000000b1"), 1760781600000)
}

// newReceiveOperation builds a one-part receive operation over the given items.
func newReceiveOperation(t *testing.T, items ...uuid.UUID) (*signedops.Operation, *domain.Passkey, *ecdsa.PrivateKey) {
	t.Helper()
	signer, key := testSignerWithKey()
	op, err := signedops.NewOperation(signedaction.TypeReceive, uuid.New(), signer, []byte(`{}`), items, receiveDetailsHash)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	return op, signer, key
}

func TestNewOperation_SplitsLargeInput(t *testing.T) {
	signer, _ := testSignerWithKey()
	createdBy := uuid.New()
	items := make([]uuid.UUID, signedops.MaxItemsPerAction+1)
	for i := range items {
		items[i] = uuid.New()
	}

	op, err := signedops.NewOperation(signedaction.TypeReceive, createdBy, signer, []byte(`{"x":1}`), items, receiveDetailsHash)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if op.OperationID.Version() != 7 {
		t.Errorf("operation id version = %d, want 7", op.OperationID.Version())
	}
	if len(op.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(op.Parts))
	}
	if len(op.Parts[0].ItemIDs) != signedops.MaxItemsPerAction || len(op.Parts[1].ItemIDs) != 1 {
		t.Fatalf("part sizes = %d + %d, want %d + 1", len(op.Parts[0].ItemIDs), len(op.Parts[1].ItemIDs), signedops.MaxItemsPerAction)
	}

	var all []uuid.UUID
	seen := map[uuid.UUID]bool{}
	salts := map[[32]byte]bool{}
	for _, p := range op.Parts {
		if p.ActionID.Version() != 7 {
			t.Errorf("action id version = %d, want 7", p.ActionID.Version())
		}
		if seen[p.ActionID] {
			t.Errorf("duplicate action id %s", p.ActionID)
		}
		seen[p.ActionID] = true
		if salts[p.Salt] {
			t.Errorf("duplicate salt in part %s", p.ActionID)
		}
		salts[p.Salt] = true
		for _, item := range p.ItemIDs {
			if seen[item] {
				t.Errorf("item %s appears twice", item)
			}
			seen[item] = true
			all = append(all, item)
		}
	}
	if len(all) != len(items) {
		t.Fatalf("items across parts = %d, want %d", len(all), len(items))
	}
	itemIDs := make([][32]byte, len(all))
	for i, id := range all {
		itemIDs[i] = signedaction.ItemID(id)
	}
	for i := 1; i < len(itemIDs); i++ {
		if bytes.Compare(itemIDs[i-1][:], itemIDs[i][:]) >= 0 {
			t.Fatalf("items not globally ascending by itemId at position %d", i)
		}
	}

	for _, p := range op.Parts {
		if p.DetailsHash != receiveDetailsHash(p.Salt, p.ItemIDs) {
			t.Errorf("part %s detailsHash mismatch", p.ActionID)
		}
		want := signedaction.Challenge(p.ActionID, signedaction.TypeReceive, signedaction.ItemsHash(p.ItemIDs), p.DetailsHash)
		if p.Challenge != want {
			t.Errorf("part %s challenge = %x, want %x", p.ActionID, p.Challenge, want)
		}
	}
	if string(op.SignerCredentialID) != "cred-signer" || op.SignerKeyID != signer.KeyID {
		t.Errorf("signer fields = %s/%x", op.SignerCredentialID, op.SignerKeyID)
	}
	if op.CreatedBy != createdBy || op.ActionType != signedaction.TypeReceive || string(op.Details) != `{"x":1}` {
		t.Errorf("operation header = %+v", op)
	}
	if op.Submitted {
		t.Errorf("fresh operation reported as submitted")
	}
}

func TestNewOperation_ZeroItems(t *testing.T) {
	signer, _ := testSignerWithKey()
	op, err := signedops.NewOperation(signedaction.TypeReceive, uuid.New(), signer, nil, nil, receiveDetailsHash)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if len(op.Parts) != 0 {
		t.Fatalf("parts = %d, want 0", len(op.Parts))
	}
}

func TestEnvelopes(t *testing.T) {
	items := []uuid.UUID{uuid.New(), uuid.New()}
	op, signer, key := newReceiveOperation(t, items...)
	part := op.Parts[0]

	assertion := signedactiontest.Sign(key, part.Challenge)
	assertion.CredentialID = signer.CredentialID
	assertions := map[uuid.UUID]signedaction.Assertion{part.ActionID: assertion}

	payloads, err := signedops.Envelopes(op, signer, assertions, signedactiontest.RPID, []string{signedactiontest.Origin})
	if err != nil {
		t.Fatalf("Envelopes: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(payloads))
	}

	var doc map[string]any
	if err := json.Unmarshal(payloads[0], &doc); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, payloads[0])
	}
	wantKeys := []string{"version", "actionId", "actionType", "itemIds", "detailsHash", "keyId", "webauthn"}
	if len(doc) != len(wantKeys) {
		t.Fatalf("envelope keys = %v, want exactly %v", doc, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := doc[k]; !ok {
			t.Errorf("envelope lacks key %q: %s", k, payloads[0])
		}
	}
	if doc["actionId"] != part.ActionID.String() || doc["actionType"] != float64(signedaction.TypeReceive) {
		t.Errorf("envelope header = %v", doc)
	}
	if doc["detailsHash"] != signedaction.Hex32(part.DetailsHash) || doc["keyId"] != signedaction.Hex32(signer.KeyID) {
		t.Errorf("envelope hashes = %v", doc)
	}
	itemIDs, ok := doc["itemIds"].([]any)
	if !ok || len(itemIDs) != len(items) {
		t.Fatalf("itemIds = %v, want %d uuids", doc["itemIds"], len(items))
	}
	for i, want := range part.ItemIDs {
		if itemIDs[i] != want.String() {
			t.Errorf("itemIds[%d] = %v, want %s", i, itemIDs[i], want)
		}
	}
}

func TestEnvelopes_Errors(t *testing.T) {
	op, signer, key := newReceiveOperation(t, uuid.New())
	part := op.Parts[0]

	validAssertion := func(t *testing.T) signedaction.Assertion {
		t.Helper()
		a := signedactiontest.Sign(key, part.Challenge)
		a.CredentialID = signer.CredentialID
		return a
	}

	t.Run("count mismatch", func(t *testing.T) {
		if _, err := signedops.Envelopes(op, signer, nil, signedactiontest.RPID, []string{signedactiontest.Origin}); !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
	})
	t.Run("missing part", func(t *testing.T) {
		if _, err := signedops.Envelopes(op, signer, map[uuid.UUID]signedaction.Assertion{}, signedactiontest.RPID, []string{signedactiontest.Origin}); !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
	})
	t.Run("wrong credential", func(t *testing.T) {
		a := validAssertion(t)
		a.CredentialID = []byte("other-credential")
		_, err := signedops.Envelopes(op, signer, map[uuid.UUID]signedaction.Assertion{part.ActionID: a}, signedactiontest.RPID, []string{signedactiontest.Origin})
		if !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		a := signedactiontest.Sign(signedactiontest.NewKey(), part.Challenge)
		a.CredentialID = signer.CredentialID
		_, err := signedops.Envelopes(op, signer, map[uuid.UUID]signedaction.Assertion{part.ActionID: a}, signedactiontest.RPID, []string{signedactiontest.Origin})
		if !errors.Is(err, signedops.ErrInvalidAssertion) {
			t.Fatalf("err = %v, want ErrInvalidAssertion", err)
		}
	})
}

func decodeSubmitRequest(t *testing.T, body string) (uuid.UUID, map[uuid.UUID]signedaction.Assertion, error) {
	t.Helper()
	var req signedops.SubmitRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal submit request: %v", err)
	}
	return req.Decode()
}

func TestSubmitRequest_Decode(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		operationID, actionID := uuid.New(), uuid.New()
		body := fmt.Sprintf(`{"operation_id":%q,"assertions":[{"action_id":%q,"credential_id":%q,"authenticator_data":%q,"client_data_json":%q,"signature":%q}]}`,
			operationID, actionID, "AA", "BB", "CC", "DD")
		gotOperationID, assertions, err := decodeSubmitRequest(t, body)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if gotOperationID != operationID {
			t.Fatalf("operation id = %s, want %s", gotOperationID, operationID)
		}
		a, ok := assertions[actionID]
		if !ok {
			t.Fatalf("no assertion for %s", actionID)
		}
		cred, _ := base64.RawURLEncoding.DecodeString("AA")
		authData, _ := base64.RawURLEncoding.DecodeString("BB")
		cdj, _ := base64.RawURLEncoding.DecodeString("CC")
		sig, _ := base64.RawURLEncoding.DecodeString("DD")
		if !bytes.Equal(a.CredentialID, cred) || !bytes.Equal(a.AuthenticatorData, authData) ||
			!bytes.Equal(a.ClientDataJSON, cdj) || !bytes.Equal(a.Signature, sig) {
			t.Errorf("assertion fields = %x/%x/%x/%x", a.CredentialID, a.AuthenticatorData, a.ClientDataJSON, a.Signature)
		}
	})
	t.Run("bad operation id", func(t *testing.T) {
		if _, _, err := decodeSubmitRequest(t, `{"operation_id":"zz","assertions":[]}`); !errors.Is(err, signedops.ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("bad action id", func(t *testing.T) {
		body := `{"operation_id":"` + uuid.NewString() + `","assertions":[{"action_id":"zz","credential_id":"AA"}]}`
		if _, _, err := decodeSubmitRequest(t, body); !errors.Is(err, signedops.ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("bad assertion base64", func(t *testing.T) {
		body := `{"operation_id":"` + uuid.NewString() + `","assertions":[{"action_id":"` + uuid.NewString() + `","credential_id":"!!!"}]}`
		if _, _, err := decodeSubmitRequest(t, body); !errors.Is(err, signedops.ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
	t.Run("duplicate action id", func(t *testing.T) {
		actionID := uuid.NewString()
		body := `{"operation_id":"` + uuid.NewString() + `","assertions":[` +
			`{"action_id":"` + actionID + `","credential_id":"AA"},` +
			`{"action_id":"` + actionID + `","credential_id":"AA"}]}`
		if _, _, err := decodeSubmitRequest(t, body); !errors.Is(err, signedops.ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
}
