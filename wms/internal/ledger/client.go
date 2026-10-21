package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Client) HealthCheck() error {
	resp, err := c.httpClient.Get(c.baseURL + "/health")
	if err != nil {
		return fmt.Errorf("ledger health check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ledger unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// RegistryKey is the adapter's view of a key in the on-chain registry. Found is
// false when the keyId is unknown to the registry.
type RegistryKey struct {
	Found  bool   `json:"found"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

// GetRegistryKeys proxies the adapter's POST /registry/keys (batched key
// status lookup). A transport error or non-200 is returned as an error.
func (c *Client) GetRegistryKeys(ctx context.Context, keyIDs []string) (map[string]RegistryKey, error) {
	if len(keyIDs) == 0 {
		return map[string]RegistryKey{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	payload, err := json.Marshal(map[string][]string{"key_ids": keyIDs})
	if err != nil {
		return nil, fmt.Errorf("ledger GetRegistryKeys marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/registry/keys", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ledger GetRegistryKeys build: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ledger GetRegistryKeys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ledger GetRegistryKeys: adapter status %d", resp.StatusCode)
	}
	var out struct {
		Keys map[string]RegistryKey `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ledger GetRegistryKeys decode: %w", err)
	}
	return out.Keys, nil
}
