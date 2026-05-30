package httputil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"wms/internal/auth"
	"wms/internal/domain"
	"wms/internal/platform/httputil"
)

// asIdentity returns a setup func that injects a fresh authenticated user with
// the given role into the request context, mirroring what the auth middleware does.
func asIdentity(role domain.UserRole) func(*http.Request) *http.Request {
	return func(r *http.Request) *http.Request {
		ctx := auth.ContextWithIdentity(r.Context(), uuid.New(), role)
		return r.WithContext(ctx)
	}
}

// noIdentity leaves the request unauthenticated (no user in context).
func noIdentity(r *http.Request) *http.Request { return r }

// decodeErrCode extracts error.code from the standard response envelope.
func decodeErrCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	if env.Success {
		t.Fatalf("expected success=false on error envelope, body=%s", body)
	}
	if env.Error == nil {
		t.Fatalf("expected non-nil error object, body=%s", body)
	}
	return env.Error.Code
}

type gate func(http.ResponseWriter, *http.Request) (uuid.UUID, bool)

type gateCase struct {
	name       string
	setup      func(*http.Request) *http.Request
	wantOK     bool
	wantStatus int
	wantCode   string
}

func runGateTable(t *testing.T, g gate, cases []gateCase) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.setup(httptest.NewRequest(http.MethodPost, "/x", http.NoBody))
			rr := httptest.NewRecorder()

			gotID, gotOK := g(rr, req)

			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if tc.wantOK {
				// On success the gate writes nothing to w, so the recorder keeps its
				// default 200 status (asserted above) and the body stays empty.
				if gotID == uuid.Nil {
					t.Fatal("expected non-nil user id on success")
				}
				if rr.Body.Len() != 0 {
					t.Fatalf("expected empty body on success, got %s", rr.Body.String())
				}
				return
			}
			if gotID != uuid.Nil {
				t.Fatalf("expected nil user id on failure, got %s", gotID)
			}
			if code := decodeErrCode(t, rr.Body.Bytes()); code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

func TestRequireOperator(t *testing.T) {
	runGateTable(t, httputil.RequireOperator, []gateCase{
		{"operator allowed", asIdentity(domain.UserRoleOperator), true, http.StatusOK, ""},
		{"admin forbidden", asIdentity(domain.UserRoleAdmin), false, http.StatusForbidden, "FORBIDDEN"},
		{"customer forbidden", asIdentity(domain.UserRoleCustomer), false, http.StatusForbidden, "FORBIDDEN"},
		{"unauthenticated", noIdentity, false, http.StatusUnauthorized, "UNAUTHORIZED"},
	})
}

func TestRequireAdminOrOperator(t *testing.T) {
	runGateTable(t, httputil.RequireAdminOrOperator, []gateCase{
		{"operator allowed", asIdentity(domain.UserRoleOperator), true, http.StatusOK, ""},
		{"admin allowed", asIdentity(domain.UserRoleAdmin), true, http.StatusOK, ""},
		{"customer forbidden", asIdentity(domain.UserRoleCustomer), false, http.StatusForbidden, "FORBIDDEN"},
		{"unauthenticated", noIdentity, false, http.StatusUnauthorized, "UNAUTHORIZED"},
	})
}
