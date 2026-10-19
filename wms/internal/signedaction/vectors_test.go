package signedaction_test

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"slices"
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
		QX                  hexBytes `json:"qx"`
		QY                  hexBytes `json:"qy"`
		Salt                hexBytes `json:"salt"`
		CargoplaceID        string   `json:"cargoplaceId"`
		StorageBinID        string   `json:"storageBinId"`
		ShippingBufferBinID string   `json:"shippingBufferBinId"`
		OrderIDs            []string `json:"orderIds"`
		OperatorID          string   `json:"operatorId"`
		OccurredAtMs        uint64   `json:"occurredAtMs"`
	} `json:"details"`
	DetailsHash hexBytes   `json:"detailsHash"`
	ItemIDs     []hexBytes `json:"itemIds"`
	ItemsHash   hexBytes   `json:"itemsHash"`
	KeyID       hexBytes   `json:"keyId"`
	Name        string     `json:"name"`
	WebAuthn    struct {
		AuthenticatorData hexBytes `json:"authenticatorData"`
		ChallengeIndex    int      `json:"challengeIndex"`
		ClientDataJSONHex hexBytes `json:"clientDataJSONHex"`
		R                 hexBytes `json:"r"`
		S                 hexBytes `json:"s"`
		TypeIndex         int      `json:"typeIndex"`
	} `json:"webauthn"`
}

type vectorProduct struct {
	ProductID string   `json:"productId"`
	ItemID    hexBytes `json:"itemId"`
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
	Products []vectorProduct `json:"products"`
	Actions  []vectorAction  `json:"actions"`
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

func findAction(v vectorsFile, name string) *vectorAction {
	for i := range v.Actions {
		if v.Actions[i].Name == name {
			return &v.Actions[i]
		}
	}
	return nil
}

func TestVectors_Items(t *testing.T) {
	v := loadVectors(t)
	receive := findAction(v, "receive")
	if receive == nil {
		t.Fatal("no receive action in vectors")
	}

	products := make([]uuid.UUID, len(v.Products))
	for i, p := range v.Products {
		id, err := uuid.Parse(p.ProductID)
		if err != nil {
			t.Fatalf("parse productId %s: %v", p.ProductID, err)
		}
		products[i] = id
		if got := signedaction.ItemID(id); got != [32]byte(p.ItemID) {
			t.Errorf("ItemID(%s) = %x, want %x", p.ProductID, got, p.ItemID)
		}
	}

	before := slices.Clone(products)
	sorted := signedaction.SortByItemID(products)
	if !slices.Equal(products, before) {
		t.Errorf("SortByItemID mutated its input")
	}
	if len(sorted) != len(receive.ItemIDs) {
		t.Fatalf("sorted len = %d, want %d", len(sorted), len(receive.ItemIDs))
	}
	for i, want := range receive.ItemIDs {
		if got := signedaction.ItemID(sorted[i]); got != [32]byte(want) {
			t.Errorf("sorted[%d] = %x, want itemId %x", i, got, want)
		}
	}
	if !slices.IsSortedFunc(sorted, func(a, b uuid.UUID) int {
		aItem := signedaction.ItemID(a)
		bItem := signedaction.ItemID(b)
		return bytes.Compare(aItem[:], bItem[:])
	}) {
		t.Errorf("SortByItemID result is not ascending by ItemID")
	}

	if got := signedaction.ItemsHash(sorted); got != [32]byte(receive.ItemsHash) {
		t.Errorf("ItemsHash = %x, want %x", got, receive.ItemsHash)
	}
	if got := signedaction.ItemsHash(nil); got != signedaction.NoItems {
		t.Errorf("ItemsHash(nil) = %x, want NoItems %x", got, signedaction.NoItems)
	}
}

func TestVectors_ReceiveDetailsHash(t *testing.T) {
	v := loadVectors(t)
	receive := findAction(v, "receive")
	if receive == nil {
		t.Fatal("no receive action in vectors")
	}
	cargoplaceID, err := uuid.Parse(receive.Details.CargoplaceID)
	if err != nil {
		t.Fatalf("parse cargoplaceId: %v", err)
	}
	operatorID, err := uuid.Parse(receive.Details.OperatorID)
	if err != nil {
		t.Fatalf("parse operatorId: %v", err)
	}
	got := signedaction.ReceiveDetailsHash([32]byte(receive.Details.Salt), cargoplaceID, operatorID, receive.Details.OccurredAtMs)
	if got != [32]byte(receive.DetailsHash) {
		t.Errorf("ReceiveDetailsHash = %x, want %x", got, receive.DetailsHash)
	}
}

func TestVectors_PutAwayDetailsHash(t *testing.T) {
	v := loadVectors(t)
	putAway := findAction(v, "putAway")
	if putAway == nil {
		t.Fatal("no putAway action in vectors")
	}
	storageBinID, err := uuid.Parse(putAway.Details.StorageBinID)
	if err != nil {
		t.Fatalf("parse storageBinId: %v", err)
	}
	operatorID, err := uuid.Parse(putAway.Details.OperatorID)
	if err != nil {
		t.Fatalf("parse operatorId: %v", err)
	}
	got := signedaction.PutAwayDetailsHash([32]byte(putAway.Details.Salt), storageBinID, operatorID, putAway.Details.OccurredAtMs)
	if got != [32]byte(putAway.DetailsHash) {
		t.Errorf("PutAwayDetailsHash = %x, want %x", got, putAway.DetailsHash)
	}
}

func TestVectors_PickDetailsHash(t *testing.T) {
	v := loadVectors(t)
	pick := findAction(v, "pick")
	if pick == nil {
		t.Fatal("no pick action in vectors")
	}
	binID, err := uuid.Parse(pick.Details.ShippingBufferBinID)
	if err != nil {
		t.Fatalf("parse shippingBufferBinId: %v", err)
	}
	operatorID, err := uuid.Parse(pick.Details.OperatorID)
	if err != nil {
		t.Fatalf("parse operatorId: %v", err)
	}
	orders := make([]uuid.UUID, 0, len(pick.Details.OrderIDs))
	for _, s := range pick.Details.OrderIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			t.Fatalf("parse orderId %s: %v", s, err)
		}
		orders = append(orders, id)
	}
	got := signedaction.PickDetailsHash([32]byte(pick.Details.Salt), binID, orders, operatorID, pick.Details.OccurredAtMs)
	if got != [32]byte(pick.DetailsHash) {
		t.Errorf("PickDetailsHash = %x, want %x", got, pick.DetailsHash)
	}

	// Дубликаты и обратный порядок дают тот же хеш; вход не мутируется.
	messy := slices.Clone(orders)
	messy = append(messy, orders...)
	slices.Reverse(messy)
	wantMessy := slices.Clone(messy)
	if got := signedaction.PickDetailsHash([32]byte(pick.Details.Salt), binID, messy, operatorID, pick.Details.OccurredAtMs); got != [32]byte(pick.DetailsHash) {
		t.Errorf("PickDetailsHash(unsorted+dupes) = %x, want %x", got, pick.DetailsHash)
	}
	if !slices.Equal(messy, wantMessy) {
		t.Errorf("PickDetailsHash mutated its input")
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
