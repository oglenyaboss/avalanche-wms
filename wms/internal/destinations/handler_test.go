package destinations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
)

// newRouter mounts the handler exactly like main.go does — under
// PathPrefix("/destinations").Subrouter() — so the test exercises the real
// gorilla/mux route matching for both "/destinations" and "/destinations/".
func newRouter(repo destinationsRepository) http.Handler {
	h := NewHandler(NewService(repo))
	r := mux.NewRouter()
	sub := r.PathPrefix("/destinations").Subrouter()
	h.RegisterRoutes(sub)
	return r
}

// asRole injects an authenticated identity into the request context, mirroring
// what auth.Middleware does after verifying a JWT.
func asRole(req *http.Request, role domain.UserRole) *http.Request {
	ctx := auth.ContextWithIdentity(req.Context(), uuid.New(), role)
	return req.WithContext(ctx)
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func strp(s string) *string { return &s }

func decodeEnv(t *testing.T, body []byte) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	return env
}

func TestHandler_GetDestinations_OperatorSuccess(t *testing.T) {
	addr := strp("г. Москва, ул. Маросейка, 5")
	repo := &fakeRepo{rows: []domain.Destination{
		{DestinationID: uuid.New(), Code: "SHOP-5", Name: "Магазин №5", Address: addr, WarehouseID: 1},
		{DestinationID: uuid.New(), Code: "SHOP-9", Name: "Магазин №9", Address: nil, WarehouseID: 2},
	}}
	router := newRouter(repo)

	// Both with and without the trailing slash must hit the handler (not 404).
	for _, path := range []string{"/destinations", "/destinations/"} {
		req := asRole(httptest.NewRequest(http.MethodGet, path, http.NoBody), domain.UserRoleOperator)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("path %s must match the route, got %d body=%s", path, rr.Code, rr.Body.String())
		}

		env := decodeEnv(t, rr.Body.Bytes())
		if !env.Success {
			t.Fatalf("expected success=true, body=%s", rr.Body.String())
		}
		if env.Error != nil {
			t.Fatalf("expected nil error, got %+v", env.Error)
		}

		var data destinationListResponse
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		if len(data.Destinations) != 2 {
			t.Fatalf("expected 2 destinations, got %d", len(data.Destinations))
		}
		if data.Destinations[0].Code != "SHOP-5" {
			t.Fatalf("code = %q, want SHOP-5", data.Destinations[0].Code)
		}
		if data.Destinations[0].Name != "Магазин №5" {
			t.Fatalf("name = %q, want Магазин №5", data.Destinations[0].Name)
		}
		if data.Destinations[0].Address == nil || *data.Destinations[0].Address != *addr {
			t.Fatalf("address mismatch: got %v, want %v", data.Destinations[0].Address, *addr)
		}
		if data.Destinations[0].WarehouseID != 1 {
			t.Fatalf("warehouse_id = %d, want 1", data.Destinations[0].WarehouseID)
		}
		// NULL address must serialize as explicit JSON null, not be omitted.
		if data.Destinations[1].Address != nil {
			t.Fatalf("expected nil address for second item, got %v", *data.Destinations[1].Address)
		}
		// Guard the wire contract directly: an omitted field and explicit null both
		// unmarshal to nil, so assert the raw body actually carries "address":null
		// (catches a stray omitempty regression that the struct check would miss).
		if !strings.Contains(rr.Body.String(), `"address":null`) {
			t.Fatalf(`expected raw body to contain "address":null, got %s`, rr.Body.String())
		}
	}
}

func TestHandler_GetDestinations_EmptyIsNonNilSlice(t *testing.T) {
	repo := &fakeRepo{rows: nil} // repo returns a nil slice
	router := newRouter(repo)

	req := asRole(httptest.NewRequest(http.MethodGet, "/destinations", http.NoBody), domain.UserRoleOperator)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// Wire shape must be {"destinations":[]}, never {"destinations":null}.
	const want = `{"success":true,"data":{"destinations":[]},"error":null}`
	got := normalizeJSON(t, rr.Body.Bytes())
	if got != normalizeJSON(t, []byte(want)) {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestHandler_GetDestinations_WarehouseFilterPassedThrough(t *testing.T) {
	repo := &fakeRepo{rows: []domain.Destination{}}
	router := newRouter(repo)

	req := asRole(httptest.NewRequest(http.MethodGet, "/destinations?warehouse_id=7", http.NoBody), domain.UserRoleOperator)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if repo.gotWarehouseID != 7 {
		t.Fatalf("warehouse_id filter = %d, want 7", repo.gotWarehouseID)
	}
}

func TestHandler_GetDestinations_BadWarehouseID(t *testing.T) {
	repo := &fakeRepo{}
	router := newRouter(repo)

	req := asRole(httptest.NewRequest(http.MethodGet, "/destinations?warehouse_id=abc", http.NoBody), domain.UserRoleOperator)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	env := decodeEnv(t, rr.Body.Bytes())
	if env.Success {
		t.Fatalf("expected success=false")
	}
	if env.Error == nil || env.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("error = %+v, want code INVALID_REQUEST", env.Error)
	}
	if env.Error.Message != "warehouse_id должен быть числом" {
		t.Fatalf("message = %q, want warehouse_id должен быть числом", env.Error.Message)
	}
}

func TestHandler_GetDestinations_NonPositiveWarehouseID(t *testing.T) {
	for _, raw := range []string{"0", "-1"} {
		repo := &fakeRepo{}
		router := newRouter(repo)

		req := asRole(httptest.NewRequest(http.MethodGet, "/destinations?warehouse_id="+raw, http.NoBody), domain.UserRoleOperator)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("warehouse_id=%s: status = %d, want 400", raw, rr.Code)
		}
		env := decodeEnv(t, rr.Body.Bytes())
		if env.Error == nil || env.Error.Code != "INVALID_REQUEST" {
			t.Fatalf("warehouse_id=%s: error = %+v, want code INVALID_REQUEST", raw, env.Error)
		}
		if repo.called {
			t.Fatalf("warehouse_id=%s: repo must not be called for invalid input", raw)
		}
	}
}

func TestHandler_GetDestinations_NonOperatorForbidden(t *testing.T) {
	repo := &fakeRepo{}
	router := newRouter(repo)

	req := asRole(httptest.NewRequest(http.MethodGet, "/destinations", http.NoBody), domain.UserRoleAdmin)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	env := decodeEnv(t, rr.Body.Bytes())
	if env.Success || env.Error == nil || env.Error.Code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN error, got success=%v error=%+v", env.Success, env.Error)
	}
}

func TestHandler_GetDestinations_Unauthenticated(t *testing.T) {
	repo := &fakeRepo{}
	router := newRouter(repo)

	// No identity injected → 401 UNAUTHORIZED from RequireOperator.
	req := httptest.NewRequest(http.MethodGet, "/destinations", http.NoBody)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	env := decodeEnv(t, rr.Body.Bytes())
	if env.Success || env.Error == nil || env.Error.Code != "UNAUTHORIZED" {
		t.Fatalf("expected UNAUTHORIZED error, got success=%v error=%+v", env.Success, env.Error)
	}
}

func TestHandler_GetDestinations_ServiceErrorIs500(t *testing.T) {
	repo := &fakeRepo{err: context.DeadlineExceeded}
	router := newRouter(repo)

	req := asRole(httptest.NewRequest(http.MethodGet, "/destinations", http.NoBody), domain.UserRoleOperator)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	env := decodeEnv(t, rr.Body.Bytes())
	if env.Success || env.Error == nil || env.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("expected INTERNAL_ERROR error, got success=%v error=%+v", env.Success, env.Error)
	}
}

// normalizeJSON re-marshals JSON so key order/whitespace differences don't
// cause false negatives in exact-shape comparisons.
func normalizeJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("normalizeJSON unmarshal: %v (raw=%s)", err, raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("normalizeJSON marshal: %v", err)
	}
	return string(out)
}
