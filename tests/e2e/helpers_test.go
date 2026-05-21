//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const itemStatusABI = `[{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"itemStatus","outputs":[{"internalType":"enum BatchMappingWMS.Status","name":"","type":"uint8"}],"stateMutability":"view","type":"function"}]`

type apiEnvelope[T any] struct {
	Success bool            `json:"success"`
	Data    T               `json:"data"`
	Error   json.RawMessage `json:"error"`
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type e2eFixture struct {
	CargoplaceID   uuid.UUID
	BoxBarcode     string
	Barcode        string
	QRCode         string
	OrderNo        string
	DispatchCode   string
	SKUID          uuid.UUID
	WarehouseID    int64
	DestinationID  uuid.UUID
	ReceivingBinID uuid.UUID
	StorageBinID   uuid.UUID
	ShippingBinID  uuid.UUID
	OrderID        uuid.UUID
	DispatchID     uuid.UUID
}

func login(t *testing.T, env *env) string {
	t.Helper()
	var pair tokenPair
	postJSON(t, env, "", "/auth/login", map[string]string{
		"username": "operator",
		"password": "operator",
	}, &pair)
	require.NotEmpty(t, pair.AccessToken)
	return pair.AccessToken
}

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

func loadSeedFixture(t *testing.T, ctx context.Context, env *env) e2eFixture {
	t.Helper()

	fixture := e2eFixture{
		BoxBarcode:   "BOX-E2E-SEED-OUTBOUND",
		Barcode:      "4600000099999",
		QRCode:       "QR-E2E-SEED-OUTBOUND",
		OrderNo:      "ORD-2026-E2E-OUTBOUND",
		DispatchCode: "DSP-2026-E2E-OUTBOUND",
	}

	err := env.db.QueryRow(ctx, `
		SELECT
			w.warehouse_id,
			d.destination_id,
			rb.bin_id,
			sb.bin_id,
			shb.bin_id,
			c.cargoplace_id,
			s.sku_id,
			o.order_id,
			od.dispatch_id
		FROM wms_inventory.warehouses w
		JOIN wms_inventory.destinations d ON d.warehouse_id = w.warehouse_id AND d.code = 'SHOP-7'
		JOIN wms_inventory.bins rb ON rb.warehouse_id = w.warehouse_id AND rb.code = 'BUFFER-01'
		JOIN wms_inventory.bins sb ON sb.warehouse_id = w.warehouse_id AND sb.code = 'A-01-01'
		JOIN wms_inventory.bins shb ON shb.warehouse_id = w.warehouse_id AND shb.code = 'BIN-SHIP-7'
		JOIN wms_inventory.inbound_shipments sh ON sh.warehouse_id = w.warehouse_id AND sh.ttn_code = 'ТТН-2026-TABLE-001'
		JOIN wms_inventory.cargoplaces c ON c.shipment_id = sh.shipment_id AND c.cargoplace_code = 'CP-TABLE-001'
		JOIN wms_inventory.sku_barcodes sbc ON sbc.barcode = $1
		JOIN wms_inventory.skus s ON s.sku_id = sbc.sku_id
		JOIN wms_inventory.orders o ON o.destination_id = d.destination_id AND o.external_order_no = $2
		JOIN wms_inventory.outbound_dispatches od ON od.destination_id = d.destination_id AND od.dispatch_code = $3
		WHERE w.name = 'Склад Москва-Север'`,
		fixture.Barcode,
		fixture.OrderNo,
		fixture.DispatchCode,
	).Scan(
		&fixture.WarehouseID,
		&fixture.DestinationID,
		&fixture.ReceivingBinID,
		&fixture.StorageBinID,
		&fixture.ShippingBinID,
		&fixture.CargoplaceID,
		&fixture.SKUID,
		&fixture.OrderID,
		&fixture.DispatchID,
	)
	require.NoError(t, err)

	return fixture
}

func eventIDForAggregate(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, aggregateType string) uuid.UUID {
	t.Helper()
	var eventID uuid.UUID
	err := env.db.QueryRow(ctx, `
		SELECT event_id
		FROM public.outbox_events
		WHERE aggregate_id = $1 AND aggregate_type = $2
		ORDER BY id DESC
		LIMIT 1`, productID, aggregateType).Scan(&eventID)
	require.NoError(t, err, "outbox event for product %s aggregate_type %s", productID, aggregateType)
	return eventID
}

func waitForOnchainCommitted(t *testing.T, ctx context.Context, env *env, eventID uuid.UUID, aggregateType string) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assertCollect) {
		var status string
		var dbAggregate string
		err := env.db.QueryRow(ctx, `
			SELECT status::text, aggregate_type
			FROM public.onchain_events
			WHERE event_id = $1`, eventID).Scan(&status, &dbAggregate)
		require.NoError(c, err)
		require.Equal(c, aggregateType, dbAggregate)
		require.Equal(c, "COMMITTED", status)
	}, 90*time.Second, 2*time.Second, "event %s did not become COMMITTED", eventID)
}

type assertCollect = assert.CollectT

func requireProductStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_inventory.products
		WHERE product_id = $1`, productID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

func requireOrderStatus(t *testing.T, ctx context.Context, env *env, orderID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_inventory.orders
		WHERE order_id = $1`, orderID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

func requireDispatchStatus(t *testing.T, ctx context.Context, env *env, dispatchID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_inventory.outbound_dispatches
		WHERE dispatch_id = $1`, dispatchID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

func requireTaskStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID, status string) {
	t.Helper()
	var got string
	err := env.db.QueryRow(ctx, `
		SELECT status::text
		FROM wms_ops.assembly_tasks
		WHERE product_id = $1`, productID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, status, got)
}

func requireOutboxCount(t *testing.T, ctx context.Context, env *env, aggregateType string, minCount int) {
	t.Helper()
	var count int
	err := env.db.QueryRow(ctx, `
		SELECT count(*)
		FROM public.outbox_events
		WHERE aggregate_type = $1`, aggregateType).Scan(&count)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, minCount)
}

func requireFSMSequence(t *testing.T, ctx context.Context, env *env, eventIDs []uuid.UUID, expected []string) {
	t.Helper()
	rows, err := env.db.Query(ctx, `
		SELECT oe.event_id, oe.aggregate_type, ce.tx_hash, ce.status::text
		FROM public.outbox_events oe
		JOIN public.onchain_events ce ON ce.event_id = oe.event_id
		WHERE oe.event_id = ANY($1::uuid[])
		ORDER BY oe.id`, eventIDs)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	seen := map[uuid.UUID]bool{}
	for rows.Next() {
		var eventID uuid.UUID
		var aggregateType, txHash, status string
		require.NoError(t, rows.Scan(&eventID, &aggregateType, &txHash, &status))
		require.Equal(t, "COMMITTED", status)
		require.NotEmpty(t, txHash)
		got = append(got, aggregateType)
		seen[eventID] = true
	}
	require.NoError(t, rows.Err())
	require.Len(t, seen, len(eventIDs))
	require.Equal(t, expected, got)
}

func callItemStatus(t *testing.T, ctx context.Context, env *env, productID uuid.UUID) uint8 {
	t.Helper()

	client, err := ethclient.DialContext(ctx, env.rpcURL)
	require.NoError(t, err)
	defer client.Close()

	parsedABI, err := abi.JSON(strings.NewReader(itemStatusABI))
	require.NoError(t, err)

	itemID := uuidToUint256(productID.String())
	data, err := parsedABI.Pack("itemStatus", itemID)
	require.NoError(t, err)

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   ptr(common.HexToAddress(env.contractAddr)),
		Data: data,
	}, nil)
	require.NoError(t, err)

	values, err := parsedABI.Unpack("itemStatus", result)
	require.NoError(t, err)
	require.Len(t, values, 1)

	status, ok := values[0].(uint8)
	require.Truef(t, ok, "unexpected itemStatus type %T", values[0])
	return status
}

func uuidToUint256(s string) *big.Int {
	return new(big.Int).SetBytes(crypto.Keccak256([]byte(s)))
}

func ptr[T any](v T) *T {
	return &v
}

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

func readSharedStateFile(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", "blockchain_project_shared_state:/shared:ro", "alpine", "cat", "/shared/"+name)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker read shared state %s: %w", name, err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("shared state %s is empty", name)
	}
	return value, nil
}
