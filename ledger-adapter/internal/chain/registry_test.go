package chain

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeCaller struct {
	out  []byte
	err  error
	call ethereum.CallMsg
}

func (f *fakeCaller) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	f.call = msg
	return f.out, f.err
}

func (f *fakeCaller) FilterLogs(_ context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}

func newTestRegistry(t *testing.T, eth contractCaller) *Registry {
	t.Helper()
	r, err := NewRegistry(eth, common.HexToAddress("0x00000000000000000000000000000000000000aa"))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestRegistry_GetKeyDecodes(t *testing.T) {
	fake := &fakeCaller{}
	r := newTestRegistry(t, fake)
	want := registryKeyTuple{
		Qx:     [32]byte{1},
		Qy:     [32]byte{2},
		OrgId:  [16]byte{3},
		Role:   2,
		Active: false,
	}
	out, err := r.abi.Methods["getKey"].Outputs.Pack(want)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	fake.out = out

	keyID := [32]byte{9}
	got, err := r.GetKey(context.Background(), keyID)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got.QX != want.Qx || got.QY != want.Qy || got.OrgID != want.OrgId || got.Role != 2 || got.Active {
		t.Fatalf("decoded = %+v", got)
	}

	wantData, _ := r.abi.Pack("getKey", keyID)
	if string(fake.call.Data) != string(wantData) {
		t.Errorf("calldata mismatch")
	}
	if fake.call.To == nil || *fake.call.To != r.addr {
		t.Errorf("call To = %v, want %v", fake.call.To, r.addr)
	}
}

func TestRegistry_GetKeyEmptyResponseIsError(t *testing.T) {
	r := newTestRegistry(t, &fakeCaller{})
	if _, err := r.GetKey(context.Background(), [32]byte{}); err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestRegistry_GetKeyCallErrorPropagates(t *testing.T) {
	boom := errors.New("rpc down")
	r := newTestRegistry(t, &fakeCaller{err: boom})
	if _, err := r.GetKey(context.Background(), [32]byte{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestRegistry_OrganizationExists(t *testing.T) {
	fake := &fakeCaller{}
	r := newTestRegistry(t, fake)
	out, err := r.abi.Methods["organizationExists"].Outputs.Pack(true)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	fake.out = out

	ok, err := r.OrganizationExists(context.Background(), [16]byte{1})
	if err != nil {
		t.Fatalf("OrganizationExists: %v", err)
	}
	if !ok {
		t.Fatal("exists = false, want true")
	}
}

func TestRegistry_OrganizationExistsEmptyResponseIsError(t *testing.T) {
	r := newTestRegistry(t, &fakeCaller{})
	if _, err := r.OrganizationExists(context.Background(), [16]byte{}); err == nil {
		t.Fatal("expected error for empty response")
	}
}
