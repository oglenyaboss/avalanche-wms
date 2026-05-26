//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestValidation_table exercises input-validation and not-found paths across
// receiving and shipping using an isolated operator token.
func TestValidation_table(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	bufferBinID := resolveBinID(t, ctx, env, "BUFFER-01") // section BUFFER, not SHIPPING_BUFFER

	t.Run("malformed JSON to scan-cargoplace -> 400 INVALID_REQUEST", func(t *testing.T) {
		status, text := postRaw(t, env, token, "/receiving/table/scan-cargoplace", `{"cargoplace_id":`)
		require.Equal(t, http.StatusBadRequest, status)
		require.Contains(t, text, "INVALID_REQUEST")
	})

	t.Run("non-uuid cargoplace_id -> 400 INVALID_REQUEST", func(t *testing.T) {
		status, code, _ := postExpectError(t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
			"cargoplace_id": "not-a-uuid",
		})
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, "INVALID_REQUEST", code)
	})

	t.Run("nonexistent cargoplace -> 404 CARGOPLACE_NOT_FOUND", func(t *testing.T) {
		status, code, _ := postExpectError(t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
			"cargoplace_id": uuid.NewString(),
		})
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "CARGOPLACE_NOT_FOUND", code)
	})

	t.Run("shipping scan-buffer with non-shipping bin -> 400 BIN_NOT_SHIPPING_BUFFER", func(t *testing.T) {
		status, code, _ := postExpectError(t, env, token, "/shipping/scan-buffer", map[string]string{
			"buffer_bin_id": bufferBinID.String(),
		})
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, "BIN_NOT_SHIPPING_BUFFER", code)
	})

	t.Run("scan-driver unknown dispatch -> 404 DISPATCH_NOT_FOUND", func(t *testing.T) {
		status, code, _ := postExpectError(t, env, token, "/shipping/scan-driver", map[string]string{
			"dispatch_code": "NOPE-DOES-NOT-EXIST",
		})
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "DISPATCH_NOT_FOUND", code)
	})
}
