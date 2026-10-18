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

func TestActionOutcomes(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	reg := newTestRegistry(t, &fakeCaller{})
	a := reg.ABI()
	keyReg := a.Events["KeyRegistered"]
	rejected := a.Events["ActionRejected"]

	idA, idB, idC, idD, idE := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	logA := &types.Log{
		Address: addr,
		// KeyRegistered(keyId indexed, orgId indexed, role, qx, qy, actionId)
		Topics: []common.Hash{keyReg.ID, common.HexToHash("0x11"), leftTopic([16]byte{})},
		Data: mustPack(t, keyReg.Inputs.NonIndexed().Pack,
			uint8(2),
			mustHex32(t, "0xbeef000000000000000000000000000000000000000000000000000000000001"),
			mustHex32(t, "0xf00d000000000000000000000000000000000000000000000000000000000002"),
			[16]byte(idA)),
	}
	logRejectedB := &types.Log{
		Address: addr,
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(idB))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(6)),
	}
	// Лог чужого контракта с тем же topic0 — не должен попасть в выборку.
	foreign := &types.Log{
		Address: common.HexToAddress("0x00000000000000000000000000000000000000bb"),
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(idC))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(9)),
	}
	logRejectedD := types.Log{
		Address: addr,
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(idD))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(5)),
	}

	fake := &fakeOutcomeCaller{
		// actionState по порядку недостающих id: C→1 (Applied), D→2 (Rejected), E→0 (None).
		states: [][]byte{actionStateOutput(t, 1), actionStateOutput(t, 2), actionStateOutput(t, 0)},
		logs:   []types.Log{logRejectedD},
	}
	reg.eth = fake

	receipt := &types.Receipt{Logs: []*types.Log{logA, logRejectedB, foreign}}

	outcomes, err := reg.ActionOutcomes(context.Background(), receipt, []uuid.UUID{idA, idB, idC, idD, idE})
	if err == nil {
		t.Fatal("expected error for id E with actionState=None")
	}
	if !strings.Contains(err.Error(), "no outcome for action "+idE.String()) {
		t.Errorf("err = %v, want no-outcome for %s", err, idE)
	}
	if outcomes != nil {
		t.Errorf("outcomes on error = %v, want nil", outcomes)
	}

	// Перезапуск без E: все четыре исхода восстанавливаются.
	fake.callN = 0
	outcomes, err = reg.ActionOutcomes(context.Background(), receipt, []uuid.UUID{idA, idB, idC, idD})
	if err != nil {
		t.Fatalf("ActionOutcomes: %v", err)
	}
	if !outcomes[idA].Applied {
		t.Errorf("idA: want Applied from KeyRegistered log, got %+v", outcomes[idA])
	}
	if outcomes[idB] != (ActionOutcome{Reason: 6}) {
		t.Errorf("idB: want Reason 6 from receipt log, got %+v", outcomes[idB])
	}
	if !outcomes[idC].Applied {
		t.Errorf("idC: want Applied from actionState=1, got %+v", outcomes[idC])
	}
	if outcomes[idD] != (ActionOutcome{Reason: 5}) {
		t.Errorf("idD: want Reason 5 from FilterLogs log, got %+v", outcomes[idD])
	}

	// Запрос FilterLogs: адрес реестра, topic0 = ActionRejected, topic1 = idD.
	if len(fake.q.Addresses) != 1 || fake.q.Addresses[0] != addr {
		t.Errorf("FilterLogs Addresses: got %v, want [%s]", fake.q.Addresses, addr)
	}
	if len(fake.q.Topics) != 2 || fake.q.Topics[0][0] != rejected.ID || fake.q.Topics[1][0] != leftTopic([16]byte(idD)) {
		t.Errorf("FilterLogs Topics: got %v", fake.q.Topics)
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
