package passkeys

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

func serveAs(t *testing.T, svc *Service, method, path string, userID uuid.UUID, role domain.UserRole) *httptest.ResponseRecorder {
	t.Helper()
	r := mux.NewRouter()
	NewHandler(svc).RegisterRoutes(r.PathPrefix("/passkeys").Subrouter())

	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), userID, role))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Success bool `json:"success"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error == nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
	}
	return env.Error.Code
}

func TestHandler_OperatorCannotViewOrganization(t *testing.T) {
	members := map[uuid.UUID]Member{operatorA: memberFixture(operatorA, orgA, "operator", domain.UserRoleOperator, "Org A")}
	svc := newTestService(t, membersRepo(members), nil)

	rec := serveAs(t, svc, http.MethodGet, "/passkeys/organization", operatorA, domain.UserRoleOperator)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_CustomerCannotBeginRegistration(t *testing.T) {
	svc := newTestService(t, membersRepo(nil), nil)

	rec := serveAs(t, svc, http.MethodPost, "/passkeys/registration/begin", uuid.New(), domain.UserRoleCustomer)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_LiveKeyConflictIs409(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	repo := membersRepo(members)
	repo.latestPasskeyFn = func(context.Context, uuid.UUID) (*domain.Passkey, error) {
		return livePasskey(adminA, orgA, domain.PasskeyStatusPending), nil
	}
	svc := newTestService(t, repo, nil)

	rec := serveAs(t, svc, http.MethodPost, "/passkeys/registration/begin", adminA, domain.UserRoleAdmin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if code := errorCode(t, rec); code != "PASSKEY_EXISTS" {
		t.Fatalf("code = %q, want PASSKEY_EXISTS", code)
	}
}

func TestHandler_MyPasskeyWithoutKeyIsNull(t *testing.T) {
	members := map[uuid.UUID]Member{adminA: memberFixture(adminA, orgA, "admin", domain.UserRoleAdmin, "Org A")}
	svc := newTestService(t, membersRepo(members), nil)

	rec := serveAs(t, svc, http.MethodGet, "/passkeys/me", adminA, domain.UserRoleAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"data":null`) {
		t.Fatalf("body = %s, want data null", rec.Body.String())
	}
}
