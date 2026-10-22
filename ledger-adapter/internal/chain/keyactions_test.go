package chain

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
)

// Структура testdata/signed-actions/vectors.v1.json (срез, нужный тесту).
type vectorsFile struct {
	Signer struct {
		KeyId string `json:"keyId"`
	} `json:"signer"`
	Actions []vectorAction `json:"actions"`
}

type vectorAction struct {
	Name            string `json:"name"`
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

// keyRegisterVector достаёт вектор keyRegister из тестовых векторов.
func keyRegisterVector(t *testing.T) (vectorsFile, vectorAction) {
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
			return v, a
		}
	}
	t.Fatal("keyRegister vector not found")
	return v, vectorAction{}
}

func TestPackApplyKeyActions_RoundTrip(t *testing.T) {
	vec, v := keyRegisterVector(t)

	want := KeyAction{
		ActionId:    [16]byte(mustHexBytes(t, v.ActionIdBytes16)),
		ActionType:  v.ActionType,
		Qx:          mustHex32(t, v.Details.Qx),
		Qy:          mustHex32(t, v.Details.Qy),
		SignerKeyId: mustHex32(t, vec.Signer.KeyId),
		Auth: WebAuthnAuth{
			R:                 mustHex32(t, v.Webauthn.R),
			S:                 mustHex32(t, v.Webauthn.S),
			ChallengeIndex:    big.NewInt(int64(v.Webauthn.ChallengeIndex)),
			TypeIndex:         big.NewInt(int64(v.Webauthn.TypeIndex)),
			AuthenticatorData: mustHexBytes(t, v.Webauthn.AuthenticatorData),
			ClientDataJSON:    string(mustHexBytes(t, v.Webauthn.ClientDataJSONHex)),
		},
	}

	data, err := PackApplyKeyActions([]KeyAction{want})
	if err != nil {
		t.Fatalf("PackApplyKeyActions: %v", err)
	}
	if !bytes.Equal(data[:4], registryABI.Methods["applyKeyActions"].ID) {
		t.Errorf("selector: got %x, want %x", data[:4], registryABI.Methods["applyKeyActions"].ID)
	}

	vals, err := registryABI.Methods["applyKeyActions"].Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	got := abi.ConvertType(vals[0], new([]KeyAction)).(*[]KeyAction)
	if len(*got) != 1 {
		t.Fatalf("unpacked %d actions, want 1", len(*got))
	}
	g := (*got)[0]
	if g.ActionId != want.ActionId {
		t.Errorf("ActionId: got %x, want %x", g.ActionId, want.ActionId)
	}
	if g.ActionType != want.ActionType {
		t.Errorf("ActionType: got %d, want %d", g.ActionType, want.ActionType)
	}
	if g.Qx != want.Qx || g.Qy != want.Qy {
		t.Errorf("Qx/Qy mismatch: got %x/%x", g.Qx, g.Qy)
	}
	if g.SignerKeyId != want.SignerKeyId {
		t.Errorf("SignerKeyId: got %x, want %x", g.SignerKeyId, want.SignerKeyId)
	}
	if g.Auth.R != want.Auth.R || g.Auth.S != want.Auth.S {
		t.Errorf("Auth.R/S mismatch")
	}
	if g.Auth.ChallengeIndex.Cmp(want.Auth.ChallengeIndex) != 0 {
		t.Errorf("ChallengeIndex: got %s, want %s", g.Auth.ChallengeIndex, want.Auth.ChallengeIndex)
	}
	if g.Auth.TypeIndex.Cmp(want.Auth.TypeIndex) != 0 {
		t.Errorf("TypeIndex: got %s, want %s", g.Auth.TypeIndex, want.Auth.TypeIndex)
	}
	if !bytes.Equal(g.Auth.AuthenticatorData, want.Auth.AuthenticatorData) {
		t.Errorf("AuthenticatorData mismatch")
	}
	if g.Auth.ClientDataJSON != want.Auth.ClientDataJSON {
		t.Errorf("ClientDataJSON: got %q, want %q", g.Auth.ClientDataJSON, want.Auth.ClientDataJSON)
	}
}

// fakeOutcomeCaller — CallContract отдаёт очередь значений actionState
// (ABI-упакованных), FilterLogs — запрограммированные логи и запоминает запрос.
type fakeOutcomeCaller struct {
	states [][]byte // очередь ответов на actionState
	callN  int
	logs   []types.Log // ответ FilterLogs
	q      ethereum.FilterQuery
}

func (f *fakeOutcomeCaller) CallContract(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	if f.callN >= len(f.states) {
		return nil, errors.New("unexpected extra CallContract")
	}
	out := f.states[f.callN]
	f.callN++
	return out, nil
}

func (f *fakeOutcomeCaller) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	f.q = q
	return f.logs, nil
}

// actionStateOutput пакует значение enum SignedAction.State для ответа actionState.
func actionStateOutput(t *testing.T, state uint8) []byte {
	t.Helper()
	out, err := registryABI.Methods["actionState"].Outputs.Pack(state)
	if err != nil {
		t.Fatalf("pack actionState output: %v", err)
	}
	return out
}

// leftTopic — indexed bytes16 в topic: value выровнен влево (первые 16 байт).
func leftTopic(id [16]byte) common.Hash {
	var h common.Hash
	copy(h[:], id[:])
	return h
}

func TestRegistry_LogOutcomes(t *testing.T) {
	reg := newTestRegistry(t, &fakeCaller{})
	a := reg.ABI()
	keyReg := a.Events["KeyRegistered"]
	rejected := a.Events["ActionRejected"]

	idA, idB, idC := uuid.New(), uuid.New(), uuid.New()

	logA := &types.Log{
		Address: reg.Address(),
		// KeyRegistered(keyId indexed, orgId indexed, role, qx, qy, actionId)
		Topics: []common.Hash{keyReg.ID, common.HexToHash("0x11"), leftTopic([16]byte{})},
		Data: mustPack(t, keyReg.Inputs.NonIndexed().Pack,
			uint8(2),
			mustHex32(t, "0xbeef000000000000000000000000000000000000000000000000000000000001"),
			mustHex32(t, "0xf00d000000000000000000000000000000000000000000000000000000000002"),
			[16]byte(idA)),
	}
	logRejectedB := &types.Log{
		Address: reg.Address(),
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(idB))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(6)),
	}
	// Лог чужого контракта с тем же topic0 — не должен попасть в выборку.
	foreign := &types.Log{
		Address: common.HexToAddress("0x00000000000000000000000000000000000000bb"),
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(idC))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(9)),
	}

	outcomes, err := reg.LogOutcomes(&types.Receipt{Logs: []*types.Log{logA, logRejectedB, foreign}})
	if err != nil {
		t.Fatalf("LogOutcomes: %v", err)
	}
	if !outcomes[idA].Applied {
		t.Errorf("idA: want Applied from KeyRegistered log, got %+v", outcomes[idA])
	}
	if outcomes[idB] != (ActionOutcome{Reason: 6}) {
		t.Errorf("idB: want Reason 6 from receipt log, got %+v", outcomes[idB])
	}
	if _, ok := outcomes[idC]; ok {
		t.Errorf("foreign-address log must be ignored, got outcome for %s", idC)
	}
}

func TestRegistry_StateOutcome(t *testing.T) {
	reg := newTestRegistry(t, &fakeCaller{})
	rejected := reg.ABI().Events["ActionRejected"]
	id := uuid.New()

	// Applied: actionState=1.
	fake := &fakeOutcomeCaller{states: [][]byte{actionStateOutput(t, 1)}}
	reg.eth = fake
	outcome, known, err := reg.StateOutcome(context.Background(), id)
	if err != nil || !known || !outcome.Applied {
		t.Fatalf("StateOutcome Applied: got (%+v, %v, %v)", outcome, known, err)
	}

	// Rejected: actionState=2 → reason из ActionRejected-лога журнала реестра.
	logRejected := types.Log{
		Address: reg.Address(),
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(id))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(5)),
	}
	fake = &fakeOutcomeCaller{states: [][]byte{actionStateOutput(t, 2)}, logs: []types.Log{logRejected}}
	reg.eth = fake
	outcome, known, err = reg.StateOutcome(context.Background(), id)
	if err != nil || !known {
		t.Fatalf("StateOutcome Rejected: got (%+v, %v, %v)", outcome, known, err)
	}
	if outcome != (ActionOutcome{Reason: 5}) {
		t.Errorf("StateOutcome Rejected: got %+v, want Reason 5", outcome)
	}

	// Запрос FilterLogs: адрес реестра, topic0 = ActionRejected, topic1 = id.
	if len(fake.q.Addresses) != 1 || fake.q.Addresses[0] != reg.Address() {
		t.Errorf("FilterLogs Addresses: got %v, want [%s]", fake.q.Addresses, reg.Address())
	}
	if len(fake.q.Topics) != 2 || fake.q.Topics[0][0] != rejected.ID || fake.q.Topics[1][0] != leftTopic([16]byte(id)) {
		t.Errorf("FilterLogs Topics: got %v", fake.q.Topics)
	}

	// None: actionState=0 — контракт действие не знает.
	fake = &fakeOutcomeCaller{states: [][]byte{actionStateOutput(t, 0)}}
	reg.eth = fake
	outcome, known, err = reg.StateOutcome(context.Background(), id)
	if err != nil || known {
		t.Fatalf("StateOutcome None: got (%+v, %v, %v), want (zero, false, nil)", outcome, known, err)
	}
	if outcome != (ActionOutcome{}) {
		t.Errorf("StateOutcome None: got %+v, want zero outcome", outcome)
	}
}

// TestApplyKeyActionsGasFloor: нижняя граница газа tx applyKeyActions = база tx +
// накладные каждого ключевого действия.
func TestApplyKeyActionsGasFloor(t *testing.T) {
	actions := func(n int) []KeyAction {
		out := make([]KeyAction, n)
		for i := range out {
			out[i] = KeyAction{ActionType: 5}
		}
		return out
	}
	cases := []struct {
		name    string
		actions []KeyAction
		want    uint64
	}{
		{"nil", nil, 21_000},
		{"one action", actions(1), 156_000},
		{"ten actions", actions(10), 1_371_000},
	}
	for _, tc := range cases {
		if got := ApplyKeyActionsGasFloor(tc.actions); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func mustPack(t *testing.T, f func(...any) ([]byte, error), args ...any) []byte {
	t.Helper()
	out, err := f(args...)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return out
}
