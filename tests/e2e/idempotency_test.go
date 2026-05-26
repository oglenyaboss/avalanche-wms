//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIdempotency_DoubleCloseCargoplace proves the FSM guard deduplicates a
// repeated close-cargoplace without a unique constraint: the second call fails
// the WHERE status='TABLE_IN_PROGRESS' predicate before the outbox INSERT, so no
// duplicate receiving event is emitted.
//
// The system relies on this guard (not a DB unique constraint) for idempotency,
// which is exactly what this test pins down.
func TestIdempotency_DoubleCloseCargoplace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Drive the receiving flow to just before close-cargoplace.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, nil)

	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)

	var product scanQRData
	postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, &product)

	postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	// Receiving outbox rows for this product before the first close.
	before := outboxCountForAggregate(t, ctx, env, product.ProductID, "receiving")

	// First close: success, exactly one receiving outbox event created.
	var closeCP closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, &closeCP)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, 1, closeCP.OutboxEventsCreated)

	afterFirst := outboxCountForAggregate(t, ctx, env, product.ProductID, "receiving")
	require.Equal(t, before+1, afterFirst, "first close should create exactly one receiving outbox event")

	// Second close: FSM-guard rejects it, no new outbox event.
	status, code, _ := postExpectError(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CARGOPLACE_NOT_IN_PROGRESS", code)

	afterSecond := outboxCountForAggregate(t, ctx, env, product.ProductID, "receiving")
	require.Equal(t, afterFirst, afterSecond, "second close must not create another receiving outbox event")
}
