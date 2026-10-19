//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIdempotency_DoubleCloseCargoplace proves the signed close is idempotent
// without a unique constraint beyond UNIQUE(operation_id, part_index): the same
// operation cannot be submitted twice (409 OPERATION_ALREADY_SUBMITTED) and a
// cargoplace already closed cannot be drafted again (409
// CARGOPLACE_NOT_IN_PROGRESS), so no duplicate outbox row is ever emitted.
func TestIdempotency_DoubleCloseCargoplace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	op := newSignedOperator(t, ctx, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Drive the receiving flow to just before close-cargoplace.
	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, nil)

	var box scanBoxData
	postJSON(t, env, op.token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	var sku scanSKUData
	postJSON(t, env, op.token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)

	var product scanQRData
	postJSON(t, env, op.token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, &product)

	postJSON[map[string]any](t, env, op.token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	// Receiving outbox rows for this product before the first close.
	before := outboxCountForAggregate(t, ctx, env, product.ProductID, "receiving")
	require.Equal(t, 0, before)

	// First close: success, exactly one signed part (one outbox event).
	draft := draftCloseCargoplace(t, env, op, fixture.CargoplaceID.String())
	assertions := signDraft(t, op, draft)
	closeCP := submitCloseCargoplace(t, env, op.token, draft.OperationID, assertions)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, 1, closeCP.OutboxEventsCreated)

	afterFirst := outboxCountForAggregate(t, ctx, env, product.ProductID, "receiving")
	require.Equal(t, 1, afterFirst, "first close should create exactly one receiving event (the signed action)")

	// Second submit of the SAME operation with the same assertions: rejected, no
	// new outbox event — the guard is the submitted_at flag, not the signatures.

	status, code, _ := postExpectError(t, env, op.token, "/receiving/table/close-cargoplace/submit", map[string]any{
		"operation_id": draft.OperationID,
		"assertions":   assertions,
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "OPERATION_ALREADY_SUBMITTED", code)

	// A new draft after the close is rejected: the cargoplace left TABLE_IN_PROGRESS.
	status, code, _ = postExpectError(t, env, op.token, "/receiving/table/close-cargoplace/draft", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "CARGOPLACE_NOT_IN_PROGRESS", code)

	afterSecond := outboxCountForAggregate(t, ctx, env, product.ProductID, "receiving")
	require.Equal(t, afterFirst, afterSecond, "neither the repeat submit nor the repeat draft may create an outbox row")
}
