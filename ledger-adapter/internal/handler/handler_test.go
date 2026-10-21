package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func doRegistryKeys(t *testing.T, reg RegistryReader, body string) (int, string) {
	t.Helper()
	mux := http.NewServeMux()
	New(reg).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/registry/keys", strings.NewReader(body)))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// multiRegistry отвечает ключами из map по запрошенному id (hex с 0x).
type multiRegistry struct {
	keys map[string]chain.RegistryKey
	err  error
}

func (m multiRegistry) GetKey(_ context.Context, id [32]byte) (chain.RegistryKey, error) {
	if m.err != nil {
		return chain.RegistryKey{}, m.err
	}
	return m.keys["0x"+hex.EncodeToString(id[:])], nil
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

func TestRegistryKeys_Batch(t *testing.T) {
	orgID := uuid.MustParse("0190f2a0-0000-7000-8000-000000000a0a")
	id1 := "0x" + strings.Repeat("a", 64)
	id2 := "0x" + strings.Repeat("b", 64)
	reg := multiRegistry{keys: map[string]chain.RegistryKey{
		id1: {OrgID: orgID, Role: chain.RoleAdmin, Active: true},
	}}
	code, body := doRegistryKeys(t, reg, fmt.Sprintf(`{"key_ids":[%q,%q]}`, id1, id2))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %s", code, body)
	}
	var got struct {
		Keys map[string]struct {
			Found  bool   `json:"found"`
			OrgID  string `json:"org_id"`
			Role   string `json:"role"`
			Active bool   `json:"active"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	k1, ok := got.Keys[id1]
	if !ok || !k1.Found || k1.Role != "admin" || !k1.Active || k1.OrgID != orgID.String() {
		t.Fatalf("keys[%s] = %+v", id1, k1)
	}
	k2, ok := got.Keys[id2]
	if !ok || k2.Found || k2.OrgID != "" || k2.Role != "" || k2.Active {
		t.Fatalf("keys[%s] = %+v, want found:false only", id2, k2)
	}
	if len(got.Keys) != 2 {
		t.Fatalf("keys = %d entries, want 2", len(got.Keys))
	}
}

func TestRegistryKeys_EmptyIDs(t *testing.T) {
	code, body := doRegistryKeys(t, multiRegistry{}, `{"key_ids":[]}`)
	if code != http.StatusBadRequest || body != `{"error":"INVALID_KEY_IDS"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKeys_TooManyIDs(t *testing.T) {
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", "0x"+strings.Repeat("a", 64))
	}
	code, body := doRegistryKeys(t, multiRegistry{}, `{"key_ids":[`+strings.Join(ids, ",")+`]}`)
	if code != http.StatusBadRequest || body != `{"error":"INVALID_KEY_IDS"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKeys_BadID(t *testing.T) {
	code, body := doRegistryKeys(t, multiRegistry{}, `{"key_ids":["`+validHash()+`","0x12"]}`)
	if code != http.StatusBadRequest || body != `{"error":"INVALID_KEY_ID"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKeys_InvalidBody(t *testing.T) {
	code, body := doRegistryKeys(t, multiRegistry{}, "nope")
	if code != http.StatusBadRequest || body != `{"error":"INVALID_BODY"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKeys_ChainError(t *testing.T) {
	reg := multiRegistry{err: errors.New("rpc down")}
	code, body := doRegistryKeys(t, reg, `{"key_ids":["`+validHash()+`"]}`)
	if code != http.StatusBadGateway || body != `{"error":"CHAIN_UNREACHABLE"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKeys_NilRegistry(t *testing.T) {
	code, body := doRegistryKeys(t, nil, `{"key_ids":["`+validHash()+`"]}`)
	if code != http.StatusBadGateway || body != `{"error":"CHAIN_UNREACHABLE"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestRegistryKeys_AllOrNothing(t *testing.T) {
	id1 := "0x" + strings.Repeat("a", 64)
	id2 := "0x" + strings.Repeat("b", 64)
	orgID := uuid.MustParse("0190f2a0-0000-7000-8000-000000000a0a")
	reg := &flakyRegistry{failOn: 2, ok: chain.RegistryKey{OrgID: orgID, Role: chain.RoleAdmin, Active: true}}
	code, body := doRegistryKeys(t, reg, fmt.Sprintf(`{"key_ids":[%q,%q]}`, id1, id2))
	if code != http.StatusBadGateway || body != `{"error":"CHAIN_UNREACHABLE"}` {
		t.Fatalf("got %d %s", code, body)
	}
	if reg.calls != 2 {
		t.Fatalf("GetKey calls = %d, want 2", reg.calls)
	}
}

// flakyRegistry отвечает ok на первые failOn-1 вызовов GetKey, дальше — ошибкой.
type flakyRegistry struct {
	failOn int
	ok     chain.RegistryKey
	calls  int
}

func (f *flakyRegistry) GetKey(_ context.Context, _ [32]byte) (chain.RegistryKey, error) {
	f.calls++
	if f.calls >= f.failOn {
		return chain.RegistryKey{}, errors.New("rpc down")
	}
	return f.ok, nil
}
