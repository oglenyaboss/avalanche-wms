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

// TxReceipt is the on-chain proof returned by the adapter's receipt endpoint.
type TxReceipt struct {
	Found         bool   `json:"found"`
	Status        string `json:"status"` // success | failed | pending
	BlockNumber   uint64 `json:"block_number"`
	Confirmations uint64 `json:"confirmations"`
	GasUsed       uint64 `json:"gas_used"`
}

// GetTxReceipt proxies the adapter's GET /onchain/tx/{hash}. A transport error
// or non-200 is returned as an error (caller maps to CHAIN_UNREACHABLE). A 200
// with found:false (tx not yet mined) is a valid, non-error result.
func (c *Client) GetTxReceipt(ctx context.Context, txHash string) (TxReceipt, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/onchain/tx/"+txHash, http.NoBody)
	if err != nil {
		return TxReceipt{}, fmt.Errorf("ledger GetTxReceipt build: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TxReceipt{}, fmt.Errorf("ledger GetTxReceipt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TxReceipt{}, fmt.Errorf("ledger GetTxReceipt: adapter status %d", resp.StatusCode)
	}
	var out TxReceipt
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return TxReceipt{}, fmt.Errorf("ledger GetTxReceipt decode: %w", err)
	}
	return out, nil
}
