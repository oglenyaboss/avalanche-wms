//go:build e2e

package e2e

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Response shapes for the read-only traceability endpoints. The e2e module cannot
// import the wms module, so these mirror the products DTO JSON tags.
type traceProductHeader struct {
	ProductID string `json:"product_id"`
	QRCode    string `json:"qr_code"`
	SKUName   string `json:"sku_name"`
	Status    string `json:"status"`
}

type traceStep struct {
	Stage       string  `json:"stage"`
	EventType   string  `json:"event_type"`
	EventID     string  `json:"event_id"`
	TxHash      *string `json:"tx_hash"`
	ChainStatus string  `json:"chain_status"`
}

type traceTimeline struct {
	Product traceProductHeader `json:"product"`
	Steps   []traceStep        `json:"steps"`
}

type traceRecentProduct struct {
	ProductID string `json:"product_id"`
	QRCode    string `json:"qr_code"`
}

// assertProductTraceability validates the traceability feature end-to-end against
// a fully-shipped product: the timeline endpoint (both resolve-by-id and
// resolve-by-qr branches), the recent list, and a live on-chain proof of one
// committed transaction against the real chain. Exercises the repository SQL that
// unit tests (which mock the repo) cannot.
func assertProductTraceability(t *testing.T, env *env, token string, productID uuid.UUID, qrCode string) {
	t.Helper()

	// Timeline by product_id — the UUID branch of resolveProduct.
	var byID traceTimeline
	getJSON(t, env, token, "/products/timeline?key="+productID.String(), &byID)
	require.Equal(t, productID.String(), byID.Product.ProductID)
	require.GreaterOrEqualf(t, len(byID.Steps), 4, "expected all four lifecycle stages, got %d", len(byID.Steps))

	stages := map[string]bool{}
	var aCommittedHash string
	for _, s := range byID.Steps {
		stages[s.Stage] = true
		// chain_status is the public.onchain_event_status enum rendered ::text —
		// uppercase (PENDING/SENT/COMMITTED/FAILED), matching the wms DTO.
		if s.ChainStatus == "COMMITTED" {
			require.NotNilf(t, s.TxHash, "committed step %q missing tx_hash", s.Stage)
			require.NotEmptyf(t, *s.TxHash, "committed step %q empty tx_hash", s.Stage)
			aCommittedHash = *s.TxHash
		}
	}
	for _, want := range []string{"receiving", "putaway", "picking", "shipping"} {
		require.Truef(t, stages[want], "timeline missing stage %q", want)
	}
	require.NotEmpty(t, aCommittedHash, "no committed step carried a tx_hash")

	// Timeline by qr_code — the text branch of resolveProduct — resolves the same product.
	var byQR traceTimeline
	getJSON(t, env, token, "/products/timeline?key="+url.QueryEscape(qrCode), &byQR)
	require.Equal(t, productID.String(), byQR.Product.ProductID)

	// Recent list includes the just-shipped product (updated_at DESC).
	var recent []traceRecentProduct
	getJSON(t, env, token, "/products/recent?limit=50", &recent)
	found := false
	for _, p := range recent {
		if p.ProductID == productID.String() {
			found = true
			break
		}
	}
	require.True(t, found, "shipped product absent from /products/recent")

	// Live on-chain proof: a committed tx must verify directly against the RPC
	// node (the auditor page checks receipts the same way, around the backend).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err, "dial rpc")
	defer client.Close()
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(aCommittedHash))
	require.NoErrorf(t, err, "receipt of committed tx: %s", aCommittedHash)
	require.Truef(t, receipt.Status == types.ReceiptStatusSuccessful, "committed tx failed on chain: %s", aCommittedHash)
	require.Greater(t, receipt.BlockNumber.Uint64(), uint64(0))
}
