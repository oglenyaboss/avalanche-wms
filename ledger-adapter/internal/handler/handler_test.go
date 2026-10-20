package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"ledger-adapter/internal/chain"
)

func validHash() string { return "0x" + strings.Repeat("a", 64) }

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
	New(reg).RegisterRoutes(mux)
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
