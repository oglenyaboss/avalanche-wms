package audit_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wms/internal/audit"
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

type vectorDetails struct {
	Salt                hexBytes `json:"salt"`
	CargoplaceID        string   `json:"cargoplaceId"`
	StorageBinID        string   `json:"storageBinId"`
	ShippingBufferBinID string   `json:"shippingBufferBinId"`
	DispatchID          string   `json:"dispatchId"`
	OrderIDs            []string `json:"orderIds"`
	OperatorID          string   `json:"operatorId"`
	OccurredAtMs        uint64   `json:"occurredAtMs"`
}

type vectorAction struct {
	Name        string        `json:"name"`
	ActionType  int           `json:"actionType"`
	Details     vectorDetails `json:"details"`
	DetailsHash hexBytes      `json:"detailsHash"`
}

type vectorsFile struct {
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

func TestDetailsHash_Vectors(t *testing.T) {
	v := loadVectors(t)
	for _, a := range v.Actions {
		if a.ActionType < 1 || a.ActionType > 4 {
			continue
		}
		d := detailsFromVector(t, a)
		got, err := d.Hash()
		if err != nil {
			t.Fatalf("%s: Hash: %v", a.Name, err)
		}
		if got != [32]byte(a.DetailsHash) {
			t.Errorf("%s: detailsHash = %x, want %x", a.Name, got, a.DetailsHash)
		}
	}
}

func TestDetailsHash_UnknownActionType(t *testing.T) {
	d := audit.Details{ActionType: 5}
	if _, err := d.Hash(); !errors.Is(err, audit.ErrUnknownActionType) {
		t.Fatalf("Details.Hash(actionType 5) = %v, want ErrUnknownActionType", err)
	}
}

func detailsFromVector(t *testing.T, a vectorAction) audit.Details {
	t.Helper()
	parse := func(field, s string) uuid.UUID {
		t.Helper()
		if s == "" {
			return uuid.UUID{}
		}
		id, err := uuid.Parse(s)
		if err != nil {
			t.Fatalf("%s: parse %s: %v", a.Name, field, err)
		}
		return id
	}
	var d audit.Details
	d.ActionType = uint8(a.ActionType)
	copy(d.Salt[:], a.Details.Salt)
	d.CargoplaceID = parse("cargoplaceId", a.Details.CargoplaceID)
	d.StorageBinID = parse("storageBinId", a.Details.StorageBinID)
	d.ShippingBufferBinID = parse("shippingBufferBinId", a.Details.ShippingBufferBinID)
	d.DispatchID = parse("dispatchId", a.Details.DispatchID)
	d.OperatorID = parse("operatorId", a.Details.OperatorID)
	for _, s := range a.Details.OrderIDs {
		d.OrderIDs = append(d.OrderIDs, parse("orderId", s))
	}
	d.OccurredAtMs = a.Details.OccurredAtMs
	return d
}
