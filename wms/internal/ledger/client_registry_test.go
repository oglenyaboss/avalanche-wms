package ledger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetRegistryKey_Found(t *testing.T) {
	keyID := "0x" + strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/keys/"+keyID {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"found":true,"org_id":"01929c5e-0000-7000-8000-00000000a001","role":"admin","active":true}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL).GetRegistryKey(context.Background(), keyID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !got.Found || !got.Active || got.Role != "admin" || got.OrgID != "01929c5e-0000-7000-8000-00000000a001" {
		t.Fatalf("unexpected key: %+v", got)
	}
}

func TestGetRegistryKey_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"found":false}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL).GetRegistryKey(context.Background(), "0x"+strings.Repeat("b", 64))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Found {
		t.Fatalf("expected not found, got %+v", got)
	}
}

func TestGetRegistryKey_BadGatewayIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"chain unreachable"}`))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).GetRegistryKey(context.Background(), "0x"+strings.Repeat("c", 64)); err == nil {
		t.Fatal("expected error on adapter 502")
	}
}
