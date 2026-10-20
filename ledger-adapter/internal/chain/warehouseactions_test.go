package chain

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
)

func TestPackApplyActions_RoundTrip(t *testing.T) {
	want := WarehouseAction{
		ActionId:    [16]byte{1, 2, 3, 4},
		ActionType:  3,
		ItemIds:     []*big.Int{big.NewInt(11), big.NewInt(22)},
		DetailsHash: [32]byte{5},
		KeyId:       [32]byte{6},
		Auth: WebAuthnAuth{
			R:                 [32]byte{7},
			S:                 [32]byte{8},
			ChallengeIndex:    big.NewInt(23),
			TypeIndex:         big.NewInt(1),
			AuthenticatorData: []byte("authdata"),
			ClientDataJSON:    `{"type":"webauthn.get"}`,
		},
	}

	data, err := PackApplyActions([]WarehouseAction{want})
	if err != nil {
		t.Fatalf("PackApplyActions: %v", err)
	}
	if !bytes.Equal(data[:4], wmsABI.Methods["applyActions"].ID) {
		t.Errorf("selector: got %x, want %x", data[:4], wmsABI.Methods["applyActions"].ID)
	}

	vals, err := wmsABI.Methods["applyActions"].Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	got := abi.ConvertType(vals[0], new([]WarehouseAction)).(*[]WarehouseAction)
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
	if len(g.ItemIds) != len(want.ItemIds) {
		t.Fatalf("ItemIds: got %d, want %d", len(g.ItemIds), len(want.ItemIds))
	}
	for i := range want.ItemIds {
		if g.ItemIds[i].Cmp(want.ItemIds[i]) != 0 {
			t.Errorf("ItemIds[%d]: got %s, want %s", i, g.ItemIds[i], want.ItemIds[i])
		}
	}
	if g.DetailsHash != want.DetailsHash {
		t.Errorf("DetailsHash: got %x, want %x", g.DetailsHash, want.DetailsHash)
	}
	if g.KeyId != want.KeyId {
		t.Errorf("KeyId: got %x, want %x", g.KeyId, want.KeyId)
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

func TestWarehouseActions_LogOutcomes(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	w := NewWarehouseActions(&fakeCaller{}, addr)
	applied := w.abi.Events["ActionApplied"]
	rejected := w.abi.Events["ActionRejected"]

	idA, idB, idC := uuid.New(), uuid.New(), uuid.New()

	logAppliedA := &types.Log{
		Address: addr,
		// ActionApplied(actionId indexed, keyId indexed, orgId indexed, actionType, detailsHash, itemCount)
		Topics: []common.Hash{applied.ID, leftTopic([16]byte(idA)), common.HexToHash("0x22"), common.HexToHash("0x33")},
		Data: mustPack(t, applied.Inputs.NonIndexed().Pack,
			uint8(1),
			mustHex32(t, "0xbeef000000000000000000000000000000000000000000000000000000000001"),
			big.NewInt(3)),
	}
	logRejectedB := &types.Log{
		Address: addr,
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(idB))},
		// ActionRejected: non-indexed reason + itemIndex.
		Data: mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(12), big.NewInt(0)),
	}
	// Лог чужого контракта с тем же topic0 — не должен попасть в выборку.
	foreign := &types.Log{
		Address: common.HexToAddress("0x00000000000000000000000000000000000000dd"),
		Topics:  []common.Hash{applied.ID, leftTopic([16]byte(idC))},
	}

	outcomes, err := w.LogOutcomes(&types.Receipt{Logs: []*types.Log{logAppliedA, logRejectedB, foreign}})
	if err != nil {
		t.Fatalf("LogOutcomes: %v", err)
	}
	if !outcomes[idA].Applied {
		t.Errorf("idA: want Applied from ActionApplied log, got %+v", outcomes[idA])
	}
	if outcomes[idB] != (ActionOutcome{Reason: 12}) {
		t.Errorf("idB: want Reason 12 from receipt log, got %+v", outcomes[idB])
	}
	if _, ok := outcomes[idC]; ok {
		t.Errorf("foreign-address log must be ignored, got outcome for %s", idC)
	}
}

func TestWarehouseActions_StateOutcome(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	rejected := wmsABI.Events["ActionRejected"]
	id := uuid.New()

	// Applied: actionState=1.
	w := NewWarehouseActions(&fakeOutcomeCaller{states: [][]byte{actionStateOutput(t, 1)}}, addr)
	outcome, known, err := w.StateOutcome(context.Background(), id)
	if err != nil || !known || !outcome.Applied {
		t.Fatalf("StateOutcome Applied: got (%+v, %v, %v)", outcome, known, err)
	}

	// Rejected: actionState=2 → reason из ActionRejected-лога журнала контракта.
	logRejected := types.Log{
		Address: addr,
		Topics:  []common.Hash{rejected.ID, leftTopic([16]byte(id))},
		Data:    mustPack(t, rejected.Inputs.NonIndexed().Pack, uint8(11), big.NewInt(2)),
	}
	fake := &fakeOutcomeCaller{states: [][]byte{actionStateOutput(t, 2)}, logs: []types.Log{logRejected}}
	w = NewWarehouseActions(fake, addr)
	outcome, known, err = w.StateOutcome(context.Background(), id)
	if err != nil || !known {
		t.Fatalf("StateOutcome Rejected: got (%+v, %v, %v)", outcome, known, err)
	}
	if outcome != (ActionOutcome{Reason: 11}) {
		t.Errorf("StateOutcome Rejected: got %+v, want Reason 11", outcome)
	}

	// Запрос FilterLogs: адрес контракта, topic0 = ActionRejected, topic1 = id.
	if len(fake.q.Addresses) != 1 || fake.q.Addresses[0] != addr {
		t.Errorf("FilterLogs Addresses: got %v, want [%s]", fake.q.Addresses, addr)
	}
	if len(fake.q.Topics) != 2 || fake.q.Topics[0][0] != rejected.ID || fake.q.Topics[1][0] != leftTopic([16]byte(id)) {
		t.Errorf("FilterLogs Topics: got %v", fake.q.Topics)
	}

	// None: actionState=0 — контракт действие не знает.
	w = NewWarehouseActions(&fakeOutcomeCaller{states: [][]byte{actionStateOutput(t, 0)}}, addr)
	outcome, known, err = w.StateOutcome(context.Background(), id)
	if err != nil || known {
		t.Fatalf("StateOutcome None: got (%+v, %v, %v), want (zero, false, nil)", outcome, known, err)
	}
	if outcome != (ActionOutcome{}) {
		t.Errorf("StateOutcome None: got %+v, want zero outcome", outcome)
	}
}

// TestApplyActionsGasFloor: нижняя граница газа tx applyActions = база tx +
// накладные каждого действия + запись на каждый товар; приёмка (ActionType 1)
// пишет новые слоты и дороже остальных переходов.
func TestApplyActionsGasFloor(t *testing.T) {
	items := func(n int) []*big.Int {
		ids := make([]*big.Int, n)
		for i := range ids {
			ids[i] = big.NewInt(int64(i + 1))
		}
		return ids
	}
	cases := []struct {
		name    string
		actions []WarehouseAction
		want    uint64
	}{
		{"nil", nil, 21_000},
		// 117 000 ≥ измеренных 108 605 газа на приёмку 1 товара (ADR-0001).
		{"receive 1 item", []WarehouseAction{{ActionType: 1, ItemIds: items(1)}}, 21_000 + 70_000 + 26_000},
		{"putaway 100 items", []WarehouseAction{{ActionType: 2, ItemIds: items(100)}}, 21_000 + 70_000 + 100*9_000},
		{"receive 3 + ship 5", []WarehouseAction{{ActionType: 1, ItemIds: items(3)}, {ActionType: 4, ItemIds: items(5)}}, 21_000 + 2*70_000 + 3*26_000 + 5*9_000},
		{"pick 0 items", []WarehouseAction{{ActionType: 3}}, 21_000 + 70_000},
	}
	for _, tc := range cases {
		if got := ApplyActionsGasFloor(tc.actions); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
