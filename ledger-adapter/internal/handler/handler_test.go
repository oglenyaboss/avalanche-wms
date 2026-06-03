package handler

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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
	})
	rec, body := doTxProof(t, h, validHash())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !body.Found || body.Status != "success" || body.BlockNumber != 100 || body.Confirmations != 7 || body.GasUsed != 64000 {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestTxProof_FailedStatus(t *testing.T) {
	h := New(stubChain{receipt: &types.Receipt{Status: types.ReceiptStatusFailed, BlockNumber: big.NewInt(5)}, head: 6})
	_, body := doTxProof(t, h, validHash())
	if body.Status != "failed" {
		t.Fatalf("status = %q, want failed", body.Status)
	}
}

func TestTxProof_NotFoundIsPending(t *testing.T) {
	h := New(stubChain{recErr: ethereum.NotFound})
	rec, body := doTxProof(t, h, validHash())
	if rec.Code != http.StatusOK || body.Found || body.Status != "pending" {
		t.Fatalf("want 200 found=false pending; got %d %+v", rec.Code, body)
	}
}

func TestTxProof_ConfirmationsClampWhenHeadLags(t *testing.T) {
	// Multi-node RPC: head answer (90) older than the receipt's block (100).
	h := New(stubChain{receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful, BlockNumber: big.NewInt(100)}, head: 90})
	_, body := doTxProof(t, h, validHash())
	if body.Confirmations != 0 {
		t.Fatalf("confirmations = %d, want 0 (clamped)", body.Confirmations)
	}
}

func TestTxProof_InvalidHash(t *testing.T) {
	h := New(stubChain{})
	rec, _ := doTxProof(t, h, "0xnothex")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
