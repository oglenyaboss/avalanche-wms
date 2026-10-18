package consumer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"maps"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"ledger-adapter/internal/chain"
)

func makeKafkaMsg(topic, eventID, productID, aggregateType string) kafka.Message {
	headers := []kafka.Header{
		{Key: "id", Value: []byte(eventID)},
	}
	if aggregateType != "" {
		headers = append(headers, kafka.Header{Key: "aggregate_type", Value: []byte(aggregateType)})
	}
	return kafka.Message{
		Topic:   topic,
		Key:     []byte(productID),
		Headers: headers,
	}
}

func TestParse_ValidReceiving(t *testing.T) {
	eventID := uuid.New()
	productID := uuid.New()
	m := makeKafkaMsg("wms.events.v1", eventID.String(), productID.String(), "receiving")

	got, err := Parse(&m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.EventID != eventID {
		t.Errorf("EventID: got %s, want %s", got.EventID, eventID)
	}
	if got.ProductID != productID {
		t.Errorf("ProductID: got %s, want %s", got.ProductID, productID)
	}
	if got.AggregateType != "receiving" {
		t.Errorf("AggregateType: got %q, want %q", got.AggregateType, "receiving")
	}
}

func TestParse_AllAggregateTypes(t *testing.T) {
	for _, agg := range []string{"receiving", "putaway", "picking", "shipping"} {
		t.Run(agg, func(t *testing.T) {
			m := makeKafkaMsg("wms.events.v1", uuid.New().String(), uuid.New().String(), agg)
			got, err := Parse(&m)
			if err != nil {
				t.Fatalf("Parse %q: %v", agg, err)
			}
			if got.AggregateType != agg {
				t.Errorf("want %q, got %q", agg, got.AggregateType)
			}
		})
	}
}

func TestParse_MissingIDHeader(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.events.v1",
		Key:   []byte(uuid.New().String()),
		Headers: []kafka.Header{
			{Key: "aggregate_type", Value: []byte("receiving")},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing 'id' header")
	}
}

func TestParse_MissingAggregateTypeHeader(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.events.v1",
		Key:   []byte(uuid.New().String()),
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(uuid.New().String())},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing 'aggregate_type' header")
	}
}

func TestParse_InvalidEventIDUUID(t *testing.T) {
	m := makeKafkaMsg("wms.events.v1", "not-a-uuid", uuid.New().String(), "receiving")
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for invalid event id UUID")
	}
}

func TestParse_MissingKey(t *testing.T) {
	m := kafka.Message{
		Topic: "wms.events.v1",
		Headers: []kafka.Header{
			{Key: "id", Value: []byte(uuid.New().String())},
			{Key: "aggregate_type", Value: []byte("receiving")},
		},
	}
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestParse_UnknownAggregateType(t *testing.T) {
	m := makeKafkaMsg("wms.events.v1", uuid.New().String(), uuid.New().String(), "unknown")
	if _, err := Parse(&m); err == nil {
		t.Fatal("expected error for unknown aggregate_type")
	}
}

// --- signed_action --------------------------------------------------------

// vectorAction / vectorsFile — срез структуры testdata/signed-actions/vectors.v1.json.
type vectorAction struct {
	Name            string `json:"name"`
	ActionId        string `json:"actionId"`
	ActionIdBytes16 string `json:"actionIdBytes16"`
	ActionType      uint8  `json:"actionType"`
	Details         struct {
		Qx string `json:"qx"`
		Qy string `json:"qy"`
	} `json:"details"`
	Webauthn struct {
		AuthenticatorData string `json:"authenticatorData"`
		ClientDataJSONHex string `json:"clientDataJSONHex"`
		R                 string `json:"r"`
		S                 string `json:"s"`
		ChallengeIndex    uint64 `json:"challengeIndex"`
		TypeIndex         uint64 `json:"typeIndex"`
	} `json:"webauthn"`
}

type vectorsFile struct {
	Signer struct {
		KeyId string `json:"keyId"`
	} `json:"signer"`
	Actions []vectorAction `json:"actions"`
}

func loadKeyRegisterVector(t *testing.T) (vectorAction, string, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/signed-actions/vectors.v1.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectorsFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	for _, a := range v.Actions {
		if a.Name == "keyRegister" {
			cdJSON, err := hex.DecodeString(strings.TrimPrefix(a.Webauthn.ClientDataJSONHex, "0x"))
			if err != nil {
				t.Fatalf("decode clientDataJSONHex: %v", err)
			}
			env := map[string]any{
				"version":    1,
				"actionId":   a.ActionId,
				"actionType": a.ActionType,
				"itemIds":    []any{},
				"subjectKey": map[string]any{"qx": a.Details.Qx, "qy": a.Details.Qy},
				"keyId":      v.Signer.KeyId,
				"webauthn": map[string]any{
					"authenticatorData": a.Webauthn.AuthenticatorData,
					"clientDataJSON":    string(cdJSON),
					"challengeIndex":    a.Webauthn.ChallengeIndex,
					"typeIndex":         a.Webauthn.TypeIndex,
					"r":                 a.Webauthn.R,
					"s":                 a.Webauthn.S,
				},
			}
			return a, v.Signer.KeyId, env
		}
	}
	t.Fatal("keyRegister vector not found")
	return vectorAction{}, "", nil
}

// encodeSignedActionValue дважды кодирует конверт: envelope → JSON-строка → байты
// Kafka value (Debezium отдаёт payload-колонку строкой).
func encodeSignedActionValue(t *testing.T, env map[string]any) []byte {
	t.Helper()
	envJSON, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	outer, err := json.Marshal(string(envJSON))
	if err != nil {
		t.Fatalf("marshal string: %v", err)
	}
	return outer
}

func signedActionKafkaMsg(t *testing.T, actionID string) kafka.Message {
	t.Helper()
	return makeKafkaMsg("wms.events.v1", actionID, actionID, "signed_action")
}

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		t.Fatalf("hex decode %q: %v", s, err)
	}
	return b
}

func mustHex32(t *testing.T, s string) [32]byte {
	t.Helper()
	b := mustHexBytes(t, s)
	if len(b) != 32 {
		t.Fatalf("want 32 bytes, got %d", len(b))
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

func assertKeyActionEqual(t *testing.T, got *chain.KeyAction, want chain.KeyAction) {
	t.Helper()
	if got.ActionId != want.ActionId {
		t.Errorf("ActionId: got %x, want %x", got.ActionId, want.ActionId)
	}
	if got.ActionType != want.ActionType {
		t.Errorf("ActionType: got %d, want %d", got.ActionType, want.ActionType)
	}
	if got.Qx != want.Qx || got.Qy != want.Qy {
		t.Errorf("Qx/Qy mismatch")
	}
	if got.SignerKeyId != want.SignerKeyId {
		t.Errorf("SignerKeyId: got %x, want %x", got.SignerKeyId, want.SignerKeyId)
	}
	if got.Auth.R != want.Auth.R || got.Auth.S != want.Auth.S {
		t.Errorf("Auth.R/S mismatch")
	}
	if got.Auth.ChallengeIndex.Cmp(want.Auth.ChallengeIndex) != 0 {
		t.Errorf("ChallengeIndex: got %s, want %s", got.Auth.ChallengeIndex, want.Auth.ChallengeIndex)
	}
	if got.Auth.TypeIndex.Cmp(want.Auth.TypeIndex) != 0 {
		t.Errorf("TypeIndex: got %s, want %s", got.Auth.TypeIndex, want.Auth.TypeIndex)
	}
	if !bytes.Equal(got.Auth.AuthenticatorData, want.Auth.AuthenticatorData) {
		t.Errorf("AuthenticatorData mismatch")
	}
	if got.Auth.ClientDataJSON != want.Auth.ClientDataJSON {
		t.Errorf("ClientDataJSON mismatch")
	}
}

func TestParse_SignedAction_KeyRegisterVector(t *testing.T) {
	vec, signerKeyID, env := loadKeyRegisterVector(t)

	actionID, err := uuid.Parse(vec.ActionId)
	if err != nil {
		t.Fatalf("parse vector actionId: %v", err)
	}
	m := signedActionKafkaMsg(t, vec.ActionId)
	m.Value = encodeSignedActionValue(t, env)

	got, err := Parse(&m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.EventID != actionID {
		t.Errorf("EventID: got %s, want %s", got.EventID, actionID)
	}
	if got.ProductID != uuid.Nil {
		t.Errorf("ProductID for action: want uuid.Nil, got %s", got.ProductID)
	}
	if got.AggregateType != "signed_action" {
		t.Errorf("AggregateType: got %q", got.AggregateType)
	}
	if got.Action == nil {
		t.Fatal("Action must be parsed for signed_action")
	}

	wantID, err := hex.DecodeString(strings.TrimPrefix(vec.ActionIdBytes16, "0x"))
	if err != nil {
		t.Fatalf("decode actionIdBytes16: %v", err)
	}
	var wantActionID [16]byte
	copy(wantActionID[:], wantID)
	want := chain.KeyAction{
		ActionId:    wantActionID,
		ActionType:  vec.ActionType,
		Qx:          mustHex32(t, vec.Details.Qx),
		Qy:          mustHex32(t, vec.Details.Qy),
		SignerKeyId: mustHex32(t, signerKeyID),
		Auth: chain.WebAuthnAuth{
			R:                 mustHex32(t, vec.Webauthn.R),
			S:                 mustHex32(t, vec.Webauthn.S),
			ChallengeIndex:    big.NewInt(int64(vec.Webauthn.ChallengeIndex)),
			TypeIndex:         big.NewInt(int64(vec.Webauthn.TypeIndex)),
			AuthenticatorData: mustHexBytes(t, vec.Webauthn.AuthenticatorData),
			ClientDataJSON:    string(mustHexBytes(t, vec.Webauthn.ClientDataJSONHex)),
		},
	}
	assertKeyActionEqual(t, got.Action, want)
}

func TestParse_SignedAction_Errors(t *testing.T) {
	_, _, env := loadKeyRegisterVector(t)
	vec := vectorAction{ActionId: env["actionId"].(string)}

	mutate := func(f func(env map[string]any)) map[string]any {
		e := maps.Clone(env)
		f(e)
		return e
	}

	cases := []struct {
		name  string
		env   map[string]any
		id    string
		key   string
		value []byte
		want  string
	}{
		{
			name: "version 2",
			env:  mutate(func(e map[string]any) { e["version"] = 2 }),
			id:   vec.ActionId,
			want: "unsupported version",
		},
		{
			name: "actionType 1",
			env:  mutate(func(e map[string]any) { e["actionType"] = 1 }),
			id:   vec.ActionId,
			want: "unsupported action type 1",
		},
		{
			name: "actionId != id header",
			env:  mutate(func(e map[string]any) { e["actionId"] = uuid.New().String() }),
			id:   vec.ActionId,
			want: "actionId",
		},
		{
			name:  "value is not a JSON string",
			env:   env,
			id:    vec.ActionId,
			value: func() []byte { b, _ := json.Marshal(env); return b }(),
			want:  "value must be a JSON string",
		},
		{
			name: "key != id header",
			env:  env,
			id:   vec.ActionId,
			key:  uuid.New().String(),
			want: "must equal id header",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value
			if value == nil {
				value = encodeSignedActionValue(t, tc.env)
			}
			id := tc.id
			key := tc.key
			if key == "" {
				key = id
			}
			m := makeKafkaMsg("wms.events.v1", id, key, "signed_action")
			m.Value = value
			_, err := Parse(&m)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}
