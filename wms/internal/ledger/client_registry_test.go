package ledger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGetRegistryKeys_PostsBatch(t *testing.T) {
	idA := "0x" + strings.Repeat("a", 64)
	idB := "0x" + strings.Repeat("b", 64)
	var gotIDs []string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/registry/keys" {
			t.Errorf("request = %s %s, want POST /registry/keys", r.Method, r.URL.Path)
		}
		gotContentType = r.Header.Get("Content-Type")
		var req struct {
			KeyIDs []string `json:"key_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotIDs = req.KeyIDs
		_, _ = w.Write([]byte(`{"keys":{"` + idA + `":{"found":true,"org_id":"01929c5e-0000-7000-8000-00000000a001","role":"admin","active":true},"` + idB + `":{"found":false}}}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL).GetRegistryKeys(context.Background(), []string{idA, idB})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(gotIDs, []string{idA, idB}) {
		t.Fatalf("sent key_ids = %v, want [%s %s]", gotIDs, idA, idB)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	a, ok := got[idA]
	if !ok || !a.Found || !a.Active || a.Role != "admin" || a.OrgID != "01929c5e-0000-7000-8000-00000000a001" {
		t.Fatalf("keys[%s] = %+v", idA, a)
	}
	b, ok := got[idB]
	if !ok || b.Found {
		t.Fatalf("keys[%s] = %+v, want not found", idB, b)
	}
}

func TestGetRegistryKeys_BadGatewayIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"chain unreachable"}`))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).GetRegistryKeys(context.Background(), []string{"0x" + strings.Repeat("c", 64)}); err == nil {
		t.Fatal("expected error on adapter 502")
	}
}

func TestGetRegistryKeys_EmptyInputSkipsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected request for empty key list")
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL).GetRegistryKeys(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty map", got)
	}
}
