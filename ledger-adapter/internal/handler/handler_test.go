package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

type stubChain struct {
	receipt *types.Receipt
	recErr  error
	head    uint64
	headErr error
}

func (s stubChain) TransactionReceipt(_ context.Context, _ common.Hash) (*types.Receipt, error) {
	return s.receipt, s.recErr
}
func (s stubChain) BlockNumber(_ context.Context) (uint64, error) { return s.head, s.headErr }

func validHash() string { return "0x" + strings.Repeat("a", 64) }

func doTxProof(t *testing.T, h *Handler, hash string) (*httptest.ResponseRecorder, txProofResponse) {
	t.Helper()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onchain/tx/"+hash, nil))
	var body txProofResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestTxProof_SuccessWithConfirmations(t *testing.T) {
	h := New(stubChain{
		receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful, BlockNumber: big.NewInt(100), GasUsed: 64000},
		head:    107,
	}, nil)
	rec, body := doTxProof(t, h, validHash())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !body.Found || body.Status != "success" || body.BlockNumber != 100 || body.Confirmations != 7 || body.GasUsed != 64000 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestTxProof_FailedStatus(t *testing.T) {
	h := New(stubChain{receipt: &types.Receipt{Status: types.ReceiptStatusFailed, BlockNumber: big.NewInt(5)}, head: 6}, nil)
	_, body := doTxProof(t, h, validHash())
	if body.Status != "failed" {
		t.Fatalf("status = %q, want failed", body.Status)
	}
}

func TestTxProof_NotFoundIsPending(t *testing.T) {
	h := New(stubChain{recErr: ethereum.NotFound}, nil)
	rec, body := doTxProof(t, h, validHash())
	if rec.Code != http.StatusOK || body.Found || body.Status != "pending" {
		t.Fatalf("want 200 found=false pending; got %d %+v", rec.Code, body)
	}
}

func TestTxProof_ConfirmationsClampWhenHeadLags(t *testing.T) {
	// Multi-node RPC: head answer (90) older than the receipt's block (100).
	h := New(stubChain{receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful, BlockNumber: big.NewInt(100)}, head: 90}, nil)
	_, body := doTxProof(t, h, validHash())
	if body.Confirmations != 0 {
		t.Fatalf("confirmations = %d, want 0 (clamped)", body.Confirmations)
	}
}

func TestTxProof_InvalidHash(t *testing.T) {
	h := New(stubChain{}, nil)
	rec, _ := doTxProof(t, h, "0xnothex")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

type stubRegistry struct {
	key chain.RegistryKey
	err error
}

func (s stubRegistry) GetKey(_ context.Context, _ [32]byte) (chain.RegistryKey, error) {
	return s.key, s.err
}

func doRegistryKey(t *testing.T, reg RegistryReader, keyID string) (int, string) {
	t.Helper()
	mux := http.NewServeMux()
	New(nil, reg).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/registry/keys/"+keyID, nil))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestRegistryKey_InvalidID(t *testing.T) {
	code, body := doRegistryKey(t, stubRegistry{}, "0xnothex")
	if code != http.StatusBadRequest || body != `{"error":"INVALID_KEY_ID"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKey_NilRegistry(t *testing.T) {
	code, body := doRegistryKey(t, nil, validHash())
	if code != http.StatusBadGateway || body != `{"error":"CHAIN_UNREACHABLE"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKey_ChainError(t *testing.T) {
	code, body := doRegistryKey(t, stubRegistry{err: errors.New("rpc down")}, validHash())
	if code != http.StatusBadGateway || body != `{"error":"CHAIN_UNREACHABLE"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKey_NotFound(t *testing.T) {
	code, body := doRegistryKey(t, stubRegistry{}, validHash())
	if code != http.StatusOK || body != `{"found":false}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKey_ActiveAdmin(t *testing.T) {
	orgID := uuid.MustParse("0190f2a0-0000-7000-8000-000000000a0a")
	reg := stubRegistry{key: chain.RegistryKey{OrgID: orgID, Role: chain.RoleAdmin, Active: true}}
	code, body := doRegistryKey(t, reg, validHash())
	want := `{"found":true,"org_id":"0190f2a0-0000-7000-8000-000000000a0a","role":"admin","active":true}`
	if code != http.StatusOK || body != want {
		t.Fatalf("got %d %s, want %s", code, body, want)
	}
}

func TestRegistryKey_RevokedOperator(t *testing.T) {
	orgID := uuid.MustParse("0190f2a0-0000-7000-8000-000000000a0a")
	reg := stubRegistry{key: chain.RegistryKey{OrgID: orgID, Role: chain.RoleOperator, Active: false}}
	code, body := doRegistryKey(t, reg, validHash())
	want := `{"found":true,"org_id":"0190f2a0-0000-7000-8000-000000000a0a","role":"operator","active":false}`
	if code != http.StatusOK || body != want {
		t.Fatalf("got %d %s, want %s", code, body, want)
	}
}
