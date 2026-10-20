package ledger

import (
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

// GetRegistryKey proxies the adapter's GET /registry/keys/{keyID}. A transport
// error or non-200 is returned as an error.
func (c *Client) GetRegistryKey(ctx context.Context, keyID string) (RegistryKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/registry/keys/"+keyID, http.NoBody)
	if err != nil {
		return RegistryKey{}, fmt.Errorf("ledger GetRegistryKey build: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return RegistryKey{}, fmt.Errorf("ledger GetRegistryKey: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RegistryKey{}, fmt.Errorf("ledger GetRegistryKey: adapter status %d", resp.StatusCode)
	}
	var out RegistryKey
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RegistryKey{}, fmt.Errorf("ledger GetRegistryKey decode: %w", err)
	}
	return out, nil
}
