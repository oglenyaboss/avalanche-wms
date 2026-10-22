package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

const (
	vecOrgID = "0190f2a0-0000-7000-8000-000000000a0a"
	vecQX    = "0x7f5426c29688122ff64414e79d553f18e05ebadb5e2b782e426c15917bedb42e"
	vecQY    = "0x1b7e077c58d025360a28537cf4c272f3b35c448dbd57ec3281524bd517558ac2"
	vecKeyID = "0x59c89f8ece38815f18358db501a0d83e949694c2bf2e183fb0de9d6344f30b8b"
)

func orgJSON(id, name, qx, qy string) []byte {
	return []byte(fmt.Sprintf(`{"orgId":%q,"name":%q,"qx":%q,"qy":%q}`, id, name, qx, qy))
}

func TestParseOrgFile_Valid(t *testing.T) {
	org, err := parseOrgFile(orgJSON(vecOrgID, "Org A", vecQX, vecQY))
	if err != nil {
		t.Fatalf("parseOrgFile: %v", err)
	}
	if org.name != "Org A" {
		t.Errorf("name = %q", org.name)
	}
	if uuid.UUID(org.orgID).String() != vecOrgID {
		t.Errorf("orgID = %s", uuid.UUID(org.orgID))
	}
	if org.keyID.Hex() != vecKeyID {
		t.Errorf("keyID = %s, want %s", org.keyID.Hex(), vecKeyID)
	}
}

func TestParseOrgFile_Invalid(t *testing.T) {
	short := "0x" + strings.Repeat("ab", 31)
	cases := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"неверный uuid", orgJSON("not-a-uuid", "Org A", vecQX, vecQY), "orgId"},
		{"qx короче 32 байт", orgJSON(vecOrgID, "Org A", short, vecQY), "qx"},
		{"точка не на кривой", orgJSON(vecOrgID, "Org A", "0x"+strings.Repeat("0", 63)+"1", "0x"+strings.Repeat("0", 63)+"1"), "не на кривой P-256"},
		{"пустое имя", orgJSON(vecOrgID, "   ", vecQX, vecQY), "name"},
		{"не JSON", []byte("{"), "org.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseOrgFile(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestRevokeKey_InvalidKeyID(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"флага нет", nil},
		{"пустой key-id", []string{"--key-id", ""}},
		{"короче 32 байт", []string{"--key-id", "0x12"}},
		{"без 0x", []string{"--key-id", strings.Repeat("ab", 32)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Валидация должна упасть до dialRegistry: RPC_URL не задан, любой
			// сетевой вызов дал бы другую ошибку.
			err := revokeKey(context.Background(), tc.args)
			if err == nil {
				t.Fatal("err = nil, want key-id validation error")
			}
			if strings.Contains(err.Error(), "dial registry") || strings.Contains(err.Error(), "RPC_URL") {
				t.Fatalf("err = %v, want a key-id validation error before any network call", err)
			}
		})
	}
}

func testABI(t *testing.T) abi.ABI {
	t.Helper()
	a, err := abi.JSON(strings.NewReader(chain.RegistryABI))
	if err != nil {
		t.Fatalf("abi: %v", err)
	}
	return a
}

func orgLog(t *testing.T, a abi.ABI, id [16]byte, name string) types.Log {
	t.Helper()
	ev := a.Events["OrganizationRegistered"]
	var topic common.Hash
	copy(topic[:16], id[:])
	data, err := ev.Inputs.NonIndexed().Pack(name)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return types.Log{Topics: []common.Hash{ev.ID, topic}, Data: data}
}

func keyLog(t *testing.T, a abi.ABI, keyID common.Hash, id [16]byte, role uint8, qx, qy [32]byte) types.Log {
	t.Helper()
	ev := a.Events["KeyRegistered"]
	var topic common.Hash
	copy(topic[:16], id[:])
	data, err := ev.Inputs.NonIndexed().Pack(role, qx, qy, [16]byte{})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return types.Log{Topics: []common.Hash{ev.ID, keyID, topic}, Data: data}
}

func revokeLog(t *testing.T, a abi.ABI, keyID common.Hash, id [16]byte) types.Log {
	t.Helper()
	ev := a.Events["KeyRevoked"]
	var topic common.Hash
	copy(topic[:16], id[:])
	return types.Log{Topics: []common.Hash{ev.ID, keyID, topic}, Data: make([]byte, 32)}
}

func TestFoldLogs(t *testing.T) {
	a := testABI(t)
	orgA := uuid.MustParse(vecOrgID)
	orgB := uuid.MustParse("0190f2a0-0000-7000-8000-000000000b0b")

	var adminQX, adminQY [32]byte
	copy(adminQX[:], common.FromHex(vecQX))
	copy(adminQY[:], common.FromHex(vecQY))
	adminKey := common.HexToHash(vecKeyID)

	var opQX, opQY [32]byte
	opQX[31], opQY[31] = 2, 3
	opKey := crypto.Keccak256Hash(opQX[:], opQY[:])

	var orphanQX, orphanQY [32]byte
	orphanQX[31], orphanQY[31] = 4, 5
	orphanKey := crypto.Keccak256Hash(orphanQX[:], orphanQY[:])

	logs := []types.Log{
		orgLog(t, a, orgA, "Org A"),
		keyLog(t, a, adminKey, orgA, 1, adminQX, adminQY),
		keyLog(t, a, opKey, orgA, 2, opQX, opQY),
		revokeLog(t, a, opKey, orgA),
		keyLog(t, a, orphanKey, orgB, 1, orphanQX, orphanQY),
	}

	got, err := foldLogs(a, logs)
	if err != nil {
		t.Fatalf("foldLogs: %v", err)
	}
	want := []org{
		{id: orgA, name: "Org A", keys: []orgKey{
			{keyID: adminKey, role: chain.RoleAdmin, active: true},
			{keyID: opKey, role: chain.RoleOperator, active: false},
		}},
		{id: orgB, name: "", keys: []orgKey{
			{keyID: orphanKey, role: chain.RoleAdmin, active: true},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foldLogs =\n%+v\nwant\n%+v", got, want)
	}
}

func TestRender(t *testing.T) {
	if got := render(nil); got != "организаций нет\n" {
		t.Fatalf("empty render = %q", got)
	}

	orgA := uuid.MustParse(vecOrgID)
	orgs := []org{
		{id: orgA, name: "Org A", keys: []orgKey{
			{keyID: common.HexToHash(vecKeyID), role: chain.RoleAdmin, active: true},
			{keyID: common.HexToHash("0x" + strings.Repeat("01", 32)), role: chain.RoleOperator, active: false},
		}},
		{id: uuid.MustParse("0190f2a0-0000-7000-8000-000000000b0b")},
	}
	want := "" +
		vecOrgID + "  Org A\n" +
		"  " + vecKeyID + "  admin     активен\n" +
		"  0x" + strings.Repeat("01", 32) + "  operator  отозван\n" +
		"0190f2a0-0000-7000-8000-000000000b0b\n"
	if got := render(orgs); got != want {
		t.Fatalf("render =\n%s\nwant\n%s", got, want)
	}
}
