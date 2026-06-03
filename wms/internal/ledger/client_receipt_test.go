package ledger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetTxReceipt_Success(t *testing.T) {
	hash := "0x" + strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/onchain/tx/"+hash {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"found":true,"status":"success","block_number":42,"confirmations":7,"gas_used":64000}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL).GetTxReceipt(context.Background(), hash)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !got.Found || got.Status != "success" || got.BlockNumber != 42 || got.Confirmations != 7 || got.GasUsed != 64000 {
		t.Fatalf("unexpected receipt: %+v", got)
	}
}

func TestGetTxReceipt_AdapterNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).GetTxReceipt(context.Background(), "0x"+strings.Repeat("b", 64)); err == nil {
		t.Fatal("expected error on adapter 500")
	}
}
