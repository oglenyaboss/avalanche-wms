//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// mintBogusAccessToken signs a structurally-valid HS256 access token (claims
// user_id/role/type=access/exp) with a secret that is NOT the WMS JWT secret, so
// the server's signature verification must reject it with 401.
func mintBogusAccessToken(t *testing.T) string {
	t.Helper()
	claims := jwt.MapClaims{
		"user_id": uuid.NewString(),
		"role":    "OPERATOR",
		"type":    "access",
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("totally-wrong-secret-not-the-real-one"))
	require.NoError(t, err)
	return signed
}

// TestAuth_NegativeAccess covers authentication and authorization failures on a
// protected receiving endpoint. The 401 cases are produced by the auth
// middleware as plain text "unauthorized" (no envelope); the 403 case is the
// handler's requireOperator guard and IS enveloped with code FORBIDDEN.
func TestAuth_NegativeAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = ctx

	env := testEnv
	require.NotNil(t, env)

	const path = "/receiving/table/scan-cargoplace"
	body := func() string { return `{"cargoplace_id":"` + uuid.NewString() + `"}` }

	// CUSTOMER token: authenticated but wrong role -> 403 FORBIDDEN (enveloped).
	customerToken := loginAs(t, env, "customer", "customer")

	t.Run("no Authorization header -> 401 plain text", func(t *testing.T) {
		status, text := postRaw(t, env, "", path, body())
		require.Equal(t, http.StatusUnauthorized, status)
		require.Contains(t, strings.ToLower(text), "unauthorized")
	})

	t.Run("garbage bearer token -> 401", func(t *testing.T) {
		status, text := postRaw(t, env, "garbage", path, body())
		require.Equal(t, http.StatusUnauthorized, status)
		require.Contains(t, strings.ToLower(text), "unauthorized")
	})

	t.Run("token signed with wrong secret -> 401", func(t *testing.T) {
		status, text := postRaw(t, env, mintBogusAccessToken(t), path, body())
		require.Equal(t, http.StatusUnauthorized, status)
		require.Contains(t, strings.ToLower(text), "unauthorized")
	})

	t.Run("valid CUSTOMER token -> 403 FORBIDDEN", func(t *testing.T) {
		status, code, _ := postExpectError(t, env, customerToken, path, map[string]string{
			"cargoplace_id": uuid.NewString(),
		})
		require.Equal(t, http.StatusForbidden, status)
		require.Equal(t, "FORBIDDEN", code)
	})
}

// TestAuth_OperatorOnlyEndpoints verifies that the putaway, assembly, and shipping
// modules enforce auth the same way receiving does. The shared auth middleware
// rejects a missing token with 401, and each handler's own requireOperator guard
// rejects a valid CUSTOMER token with 403 FORBIDDEN. TestAuth_NegativeAccess already
// covers the receiving endpoint plus the garbage/wrong-secret 401 variants (which
// are middleware-level and identical for every protected route), so this test closes
// the per-module operator-guard gap the bug-hunt found on putaway/assembly/shipping.
//
// requireOperator runs before any body parsing in every handler, so the dummy bodies
// here never reach validation: the 401/403 is decided purely by the token.
func TestAuth_OperatorOnlyEndpoints(t *testing.T) {
	env := testEnv
	require.NotNil(t, env)

	customerToken := loginAs(t, env, "customer", "customer")

	endpoints := []struct {
		name string
		path string
		body map[string]string
	}{
		{"putaway scan-buffer", "/putaway/scan-buffer", map[string]string{"buffer_bin_id": uuid.NewString()}},
		{"putaway scan-product", "/putaway/scan-product", map[string]string{"product_id": uuid.NewString(), "buffer_bin_id": uuid.NewString()}},
		{"putaway scan-storage-bin", "/putaway/scan-storage-bin", map[string]string{"storage_bin_id": uuid.NewString()}},
		{"assembly allocate", "/assembly/allocate", map[string]string{"destination_id": uuid.NewString()}},
		{"assembly pick", "/assembly/pick", map[string]string{"product_id": uuid.NewString()}},
		{"assembly scan-shipping-buffer", "/assembly/scan-shipping-buffer", map[string]string{"buffer_bin_id": uuid.NewString()}},
		{"shipping scan-buffer", "/shipping/scan-buffer", map[string]string{"buffer_bin_id": uuid.NewString()}},
		{"shipping scan-driver", "/shipping/scan-driver", map[string]string{"dispatch_code": "DSP-AUTH-" + uuid.NewString()[:8]}},
		{"shipping ship", "/shipping/ship", map[string]string{"buffer_bin_id": uuid.NewString(), "dispatch_id": uuid.NewString()}},
	}

	for _, ep := range endpoints {
		ep := ep
		t.Run(ep.name+" no token -> 401", func(t *testing.T) {
			// Body is irrelevant: the middleware rejects before the handler runs.
			status, text := postRaw(t, env, "", ep.path, "{}")
			require.Equal(t, http.StatusUnauthorized, status)
			require.Contains(t, strings.ToLower(text), "unauthorized")
		})
		t.Run(ep.name+" CUSTOMER -> 403 FORBIDDEN", func(t *testing.T) {
			status, code, _ := postExpectError(t, env, customerToken, ep.path, ep.body)
			require.Equal(t, http.StatusForbidden, status)
			require.Equal(t, "FORBIDDEN", code)
		})
	}
}
