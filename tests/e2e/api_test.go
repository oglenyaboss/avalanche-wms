//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// apiEnvelope models the standard WMS response envelope: {success, data, error}.
// Auth endpoints (/auth/*) return raw JSON without this envelope.
type apiEnvelope[T any] struct {
	Success bool            `json:"success"`
	Data    T               `json:"data"`
	Error   json.RawMessage `json:"error"`
}

// apiError models the error object inside the envelope.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// ── Response DTOs for the outbound flow ────────────────────────────────────

type scanBoxData struct {
	BoxID  uuid.UUID `json:"box_id"`
	Status string    `json:"status"`
}

type scanSKUData struct {
	SKUID uuid.UUID `json:"sku_id"`
}

type scanQRData struct {
	ProductID uuid.UUID `json:"product_id"`
	Status    string    `json:"status"`
}

type closeCargoplaceData struct {
	OutboxEventsCreated int    `json:"outbox_events_created"`
	Status              string `json:"status"`
}

type scanStorageBinData struct {
	OutboxEventsCreated int `json:"outbox_events_created"`
	ProductsPlaced      int `json:"products_placed"`
}

type allocateData struct {
	AllocatedOrders    int                     `json:"allocated_orders"`
	AllocatedProducts  int                     `json:"allocated_products"`
	InsufficientOrders []insufficientOrderData `json:"insufficient_orders"`
}

type insufficientOrderData struct {
	OrderID     string                `json:"order_id"`
	MissingSKUs []insufficientSKUData `json:"missing_skus"`
}

type insufficientSKUData struct {
	SKUID      string `json:"sku_id"`
	SKUName    string `json:"sku_name"`
	MissingQty int    `json:"missing_qty"`
}

type taskListData struct {
	Tasks []taskData `json:"tasks"`
}

type taskData struct {
	TaskID    string `json:"task_id"`
	ProductID string `json:"product_id"`
}

type pickData struct {
	ProductID string `json:"product_id"`
	CartSize  int    `json:"cart_size"`
}

type scanShippingBufferData struct {
	ProductsPlaced  int `json:"products_placed"`
	OrdersAssembled int `json:"orders_assembled"`
}

type scanDriverData struct {
	DispatchID uuid.UUID `json:"dispatch_id"`
	Status     string    `json:"status"`
}

type shipData struct {
	ProductsShipped     int  `json:"products_shipped"`
	OutboxEventsCreated int  `json:"outbox_events_created"`
	OrdersCompleted     int  `json:"orders_completed"`
	DispatchDeparted    bool `json:"dispatch_departed"`
	BufferRemaining     int  `json:"buffer_remaining"`
}

// productIDsFromTasks extracts product ids from a task list response.
func productIDsFromTasks(tasks []taskData) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ProductID)
	}
	return ids
}

// ── Auth helpers ───────────────────────────────────────────────────────────

// login authenticates as the seeded operator and returns its access token.
func login(t *testing.T, env *env) string {
	t.Helper()
	return loginAs(t, env, "operator", "operator")
}

// loginAs authenticates as an arbitrary user and returns its access token.
func loginAs(t *testing.T, env *env, user, pass string) string {
	t.Helper()
	var pair tokenPair
	postJSON(t, env, "", "/auth/login", map[string]string{
		"username": user,
		"password": pass,
	}, &pair)
	require.NotEmpty(t, pair.AccessToken, "login as %s returned empty access token", user)
	return pair.AccessToken
}

// registerOperator creates an OPERATOR user via the admin-only /auth/register
// endpoint. It tolerates both 201 (created) and 409 (already exists).
func registerOperator(t *testing.T, env *env, adminToken, username string) {
	t.Helper()

	status, body := postRaw(t, env, adminToken, "/auth/register", fmt.Sprintf(
		`{"username":%q,"password":"e2e-pass-123","role":"OPERATOR"}`, username))
	require.Containsf(t, []int{http.StatusCreated, http.StatusConflict}, status,
		"register %s status %d: %s", username, status, body)
}

// operatorToken provisions a fresh, uniquely-named OPERATOR and returns its
// access token. Assembly carts are keyed per-operator in-memory in WMS, so each
// state-changing test needs its own operator to avoid cross-test cart leakage.
func operatorToken(t *testing.T, env *env) string {
	t.Helper()
	adminToken := loginAs(t, env, "admin", "admin")
	username := "op-" + uuid.NewString()[:8]
	registerOperator(t, env, adminToken, username)
	return loginAs(t, env, username, "e2e-pass-123")
}

// ── HTTP request helpers ───────────────────────────────────────────────────

// postJSON sends a JSON POST and requires HTTP 200. For /auth/* paths it decodes
// the raw body into out; otherwise it unwraps the success envelope into out.
func postJSON[T any](t *testing.T, env *env, token, path string, body any, out *T) {
	t.Helper()

	data, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, env.wmsURL+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := env.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "POST %s response: %s", path, string(respBody))

	if out == nil {
		return
	}
	if strings.HasPrefix(path, "/auth/") {
		require.NoError(t, json.Unmarshal(respBody, out), "POST %s response: %s", path, string(respBody))
		return
	}

	var envelope apiEnvelope[T]
	require.NoError(t, json.Unmarshal(respBody, &envelope), "POST %s response: %s", path, string(respBody))
	require.Truef(t, envelope.Success, "POST %s API error: %s", path, string(respBody))
	*out = envelope.Data
}

// getJSON sends a GET and unwraps the success envelope into out.
func getJSON[T any](t *testing.T, env *env, token, path string, out *T) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, env.wmsURL+path, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := env.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s response: %s", path, string(respBody))

	var envelope apiEnvelope[T]
	require.NoError(t, json.Unmarshal(respBody, &envelope), "GET %s response: %s", path, string(respBody))
	require.Truef(t, envelope.Success, "GET %s API error: %s", path, string(respBody))
	*out = envelope.Data
}

// postExpectError sends a JSON POST that is expected to fail, decodes the error
// envelope, and returns the HTTP status, error.code, and error.message. It does
// NOT require any particular status code.
func postExpectError(t *testing.T, env *env, token, path string, body any) (status int, code, message string) {
	t.Helper()

	data, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, env.wmsURL+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := env.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var envelope struct {
		Success bool      `json:"success"`
		Error   *apiError `json:"error"`
	}
	require.NoErrorf(t, json.Unmarshal(respBody, &envelope), "POST %s response: %s", path, string(respBody))
	require.NotNilf(t, envelope.Error, "POST %s expected error envelope, got: %s", path, string(respBody))
	return resp.StatusCode, envelope.Error.Code, envelope.Error.Message
}

// postRaw sends raw request bytes (for malformed-JSON or unknown-field tests)
// and returns the HTTP status plus the raw response body as text.
func postRaw(t *testing.T, env *env, token, path, rawBody string) (status int, bodyText string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, env.wmsURL+path, strings.NewReader(rawBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := env.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(respBody)
}

// waitForHTTP polls url until it returns wantStatus or timeout elapses.
func waitForHTTP(ctx context.Context, client *http.Client, url string, wantStatus int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == wantStatus {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s: %w", url, lastErr)
}

// ── Debezium connector helpers ─────────────────────────────────────────────

func registerDebeziumConnector(ctx context.Context, env *env) error {
	configPath := filepath.Join(env.repoRoot, "deploy/debezium/connectors/postgres-connector.json")
	body, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read connector config: %w", err)
	}

	if err := waitForHTTP(ctx, env.httpClient, env.debeziumURL+"/connectors", http.StatusOK, 2*time.Minute); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.debeziumURL+"/connectors", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("register connector status %d: %s", resp.StatusCode, string(respBody))
}

func waitForDebeziumConnector(ctx context.Context, env *env, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.debeziumURL+"/connectors/outbox-connector/status", nil)
		if err != nil {
			return err
		}
		resp, err := env.httpClient.Do(req)
		if err == nil {
			var payload struct {
				Connector struct {
					State string `json:"state"`
				} `json:"connector"`
				Tasks []struct {
					State string `json:"state"`
				} `json:"tasks"`
			}
			if resp.StatusCode == http.StatusOK {
				err = json.NewDecoder(resp.Body).Decode(&payload)
			}
			_ = resp.Body.Close()
			if err == nil && payload.Connector.State == "RUNNING" && len(payload.Tasks) > 0 && payload.Tasks[0].State == "RUNNING" {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for debezium connector RUNNING")
}
