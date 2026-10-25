package dispatches

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"wms/internal/auth"
	"wms/internal/domain"
)

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *ApiError       `json:"error"`
}

func serve(t *testing.T, repo *mockDispatchesRepo, method, target, body string) (int, envelope) {
	t.Helper()
	r := mux.NewRouter()
	NewHandler(NewService(repo)).RegisterRoutes(r.PathPrefix("/dispatches").Subrouter())

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), uuid.New(), domain.UserRoleOperator))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, rec.Body.String())
	}
	return rec.Code, env
}

func assertError(t *testing.T, gotStatus int, env envelope, wantStatus int, wantCode, wantMessage string) {
	t.Helper()
	if gotStatus != wantStatus {
		t.Fatalf("status = %d, want %d", gotStatus, wantStatus)
	}
	if env.Error == nil || env.Error.Code != wantCode || env.Error.Message != wantMessage {
		t.Fatalf("error = %+v, want {%s %s}", env.Error, wantCode, wantMessage)
	}
}

func TestHandler_GetDispatches_EmptyResultIsArray(t *testing.T) {
	repo := &mockDispatchesRepo{getDispatchesByFilterFn: func(context.Context, DispatchFilter) ([]domain.OutboundDispatch, error) {
		return []domain.OutboundDispatch{}, nil
	}}

	status, env := serve(t, repo, http.MethodGet, "/dispatches/", "")

	if status != http.StatusOK || string(env.Data) != "[]" {
		t.Fatalf("status = %d, data = %s, want 200 []", status, env.Data)
	}
}

func TestHandler_GetDispatches_KnownStatusPassesToFilter(t *testing.T) {
	var got DispatchFilter
	repo := &mockDispatchesRepo{getDispatchesByFilterFn: func(_ context.Context, filter DispatchFilter) ([]domain.OutboundDispatch, error) {
		got = filter
		return []domain.OutboundDispatch{}, nil
	}}

	status, _ := serve(t, repo, http.MethodGet, "/dispatches/?status=AT_GATE", "")

	if status != http.StatusOK || got.Status == nil || *got.Status != domain.OutboundDispatchStatusAtGate {
		t.Fatalf("status = %d, filter.Status = %v, want 200 AT_GATE", status, got.Status)
	}
}

func TestHandler_GetDispatches_InvalidQuery(t *testing.T) {
	cases := []struct {
		name, query, message string
	}{
		{"неизвестный статус", "status=BOGUS", "неизвестный статус рейса"},
		{"статус в нижнем регистре", "status=at_gate", "неизвестный статус рейса"},
		{"destination_id не uuid", "destination_id=1", "destination_id не uuid"},
		{"warehouse_id не число", "warehouse_id=abc", "warehouse_id не число"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, env := serve(t, &mockDispatchesRepo{}, http.MethodGet, "/dispatches/?"+tc.query, "")
			assertError(t, status, env, http.StatusBadRequest, "INVALID_REQUEST", tc.message)
		})
	}
}

func TestHandler_GetDispatchByID_Errors(t *testing.T) {
	t.Run("dispatch_id не uuid", func(t *testing.T) {
		status, env := serve(t, &mockDispatchesRepo{}, http.MethodGet, "/dispatches/1", "")
		assertError(t, status, env, http.StatusBadRequest, "INVALID_REQUEST", "dispatch_id не uuid")
	})

	t.Run("рейс не найден", func(t *testing.T) {
		repo := &mockDispatchesRepo{getDispatchByIDFn: func(context.Context, uuid.UUID) (domain.OutboundDispatch, error) {
			return domain.OutboundDispatch{}, ErrDispatchNotFound
		}}
		status, env := serve(t, repo, http.MethodGet, "/dispatches/"+uuid.NewString(), "")
		assertError(t, status, env, http.StatusNotFound, "DISPATCH_NOT_FOUND", "Рейс не найден")
	})
}

func TestHandler_NewDispatch_Errors(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := func(driver, vehicle, scheduled string) string {
		return `{"destination_id":"` + uuid.NewString() + `","driver_name":"` + driver + `","vehicle_number":"` + vehicle + `","scheduled_at":"` + scheduled + `"}`
	}
	cases := []struct {
		name, body, message string
	}{
		{"невалидный JSON", "{", "Невалидный JSON в теле запроса"},
		{"лишнее поле", `{"extra":1}`, "Невалидный JSON в теле запроса"},
		{"нет driver_name", body("", "А123ВС77", future), "driver_name обязателен"},
		{"нет vehicle_number", body("Иванов", "", future), "vehicle_number обязателен"},
		{"scheduled_at в прошлом", body("Иванов", "А123ВС77", past), "scheduled_at не может быть в прошлом"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, env := serve(t, &mockDispatchesRepo{}, http.MethodPost, "/dispatches/", tc.body)
			assertError(t, status, env, http.StatusBadRequest, "INVALID_REQUEST", tc.message)
		})
	}

	t.Run("магазин не найден", func(t *testing.T) {
		repo := &mockDispatchesRepo{createDispatchCodeFn: func(context.Context) (string, error) {
			return "DSP-2026-1025-001", nil
		}, createNewDispatchFn: func(context.Context, *NewDispatchQuery, string) (domain.OutboundDispatch, error) {
			return domain.OutboundDispatch{}, ErrDestinationNotFound
		}}
		status, env := serve(t, repo, http.MethodPost, "/dispatches/", body("Иванов", "А123ВС77", future))
		assertError(t, status, env, http.StatusNotFound, "DESTINATION_NOT_FOUND", "Магазин не найден")
	})
}
