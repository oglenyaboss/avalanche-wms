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

// driveReceivingToBuffer runs the full receiving-table flow for a single-product
// outbound fixture: scan-cargoplace -> scan-box -> scan-sku -> scan-qr ->
// close-box -> scan-buffer -> close-cargoplace. It returns the product ID of the
// newly RECEIVED product sitting in BUFFER-01.
func driveReceivingToBuffer(t *testing.T, ctx context.Context, env *env, token string, fixture e2eFixture) uuid.UUID {
	t.Helper()

	// The close is a signed action now: this helper provisions its own signed
	// operator and drives the whole receiving flow with its token.
	op := newSignedOperator(t, ctx, env)

	// scan cargoplace at table
	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, nil)

	// scan box
	var box scanBoxData
	postJSON(t, env, op.token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)

	// scan sku
	var sku scanSKUData
	postJSON(t, env, op.token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)

	// scan qr -> creates the product
	var product scanQRData
	postJSON(t, env, op.token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, &product)

	// close box
	postJSON[map[string]any](t, env, op.token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	// scan buffer
	postJSON[map[string]any](t, env, op.token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	// close cargoplace (signed)
	closeCargoplaceSigned(t, env, op, fixture.CargoplaceID.String())

	return product.ProductID
}

// TestPutaway_DoublePlace_Conflict drives a product through receiving into
// BUFFER-01, places it into storage (STORED), then retries the putaway draft with
// the same product. The second call must fail with 409 PRODUCT_NOT_IN_BUFFER and
// must not emit an additional putaway outbox event.
func TestPutaway_DoublePlace_Conflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Drive the product through the full receiving flow.
	productID := driveReceivingToBuffer(t, ctx, env, token, fixture)
	requireProductStatus(t, ctx, env, productID, "RECEIVED")

	// Placement is a signed action: the placing operator must hold an active passkey.
	op := newSignedOperator(t, ctx, env)

	// Putaway: scan buffer, scan product, place into storage bin.
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-product", map[string]string{
		"product_id":    productID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	putaway := placeToStorageBinSigned(t, env, op, []uuid.UUID{productID}, fixture.StorageBinID)
	require.Equal(t, 1, putaway.ProductsPlaced)
	require.Equal(t, 1, putaway.OutboxEventsCreated, "one product places with exactly one signed part")
	requireProductStatus(t, ctx, env, productID, "STORED")

	// Snapshot outbox count after the first (successful) putaway.
	outboxBefore := outboxCountForAggregate(t, ctx, env, productID, "putaway")
	require.Equal(t, 1, outboxBefore, "first putaway should create exactly one signed action event")

	// Second putaway attempt with the same product (now STORED, not in buffer):
	// the DRAFT fails, so no operation is even created.
	status, code, _ := draftPlaceToStorageBinRaw(t, env, op.token, []string{productID.String()}, fixture.StorageBinID.String())
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "PRODUCT_NOT_IN_BUFFER", code)

	// No additional outbox event created.
	outboxAfter := outboxCountForAggregate(t, ctx, env, productID, "putaway")
	require.Equal(t, outboxBefore, outboxAfter, "second putaway must not create another outbox event")
}

// TestPutaway_PlaceIntoBufferBin_Rejected verifies the storage-bin type guard:
// placing a RECEIVED product into a BUFFER-section bin or a SHIPPING_BUFFER-section
// bin must be rejected with 404 STORAGE_BIN_NOT_FOUND.
func TestPutaway_PlaceIntoBufferBin_Rejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Drive the product through receiving so it is RECEIVED in BUFFER-01.
	productID := driveReceivingToBuffer(t, ctx, env, token, fixture)
	requireProductStatus(t, ctx, env, productID, "RECEIVED")

	// Placement is a signed action: the draft validates the storage bin before
	// anything is signed or written.
	op := newSignedOperator(t, ctx, env)

	// Prepare the putaway session: scan buffer + scan product.
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-product", map[string]string{
		"product_id":    productID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	t.Run("BUFFER bin rejected", func(t *testing.T) {
		status, code, _ := draftPlaceToStorageBinRaw(t, env, op.token, []string{productID.String()}, fixture.ReceivingBinID.String()) // BUFFER-01 (section BUFFER)
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "STORAGE_BIN_NOT_FOUND", code)
	})

	t.Run("SHIPPING_BUFFER bin rejected", func(t *testing.T) {
		status, code, _ := draftPlaceToStorageBinRaw(t, env, op.token, []string{productID.String()}, fixture.ShippingBinID.String()) // BIN-SHIP-7 (section SHIPPING_BUFFER)
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "STORAGE_BIN_NOT_FOUND", code)
	})

	// Product must still be RECEIVED — neither attempt should have mutated it.
	requireProductStatus(t, ctx, env, productID, "RECEIVED")
}

// TestPutaway_ScanProduct_AlreadyStored verifies that POST /putaway/scan-product
// rejects a product that is already STORED with 409 PRODUCT_NOT_RECEIVED.
func TestPutaway_ScanProduct_AlreadyStored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	bufferBinID := resolveBinID(t, ctx, env, "BUFFER-01")

	// Create a pre-STORED product via the multi-product fixture.
	mpFixture := newMultiProductFixture(t, ctx, env, destinationCode, 1, 1)
	require.Len(t, mpFixture.ProductIDs, 1)
	storedProductID := mpFixture.ProductIDs[0]
	requireProductStatus(t, ctx, env, storedProductID, "STORED")

	// Attempt to scan the already-STORED product in the putaway flow.
	status, code, _ := postExpectError(t, env, token, "/putaway/scan-product", map[string]string{
		"product_id":    storedProductID.String(),
		"buffer_bin_id": bufferBinID.String(),
	})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "PRODUCT_NOT_IN_BUFFER", code)
}

// TestPutaway_PlaceWithBogusProduct_Rollback verifies transactional atomicity: when
// The putaway draft receives a mix of a valid RECEIVED product (P1) and a nonexistent
// product UUID, the entire operation must roll back. P1 must remain RECEIVED, and no
// putaway outbox event should be emitted for P1.
func TestPutaway_PlaceWithBogusProduct_Rollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	token := operatorToken(t, env)
	fixture := newOutboundFixture(t, ctx, env)

	// Drive P1 through receiving so it is RECEIVED in BUFFER-01.
	productID := driveReceivingToBuffer(t, ctx, env, token, fixture)
	requireProductStatus(t, ctx, env, productID, "RECEIVED")

	// Placement is a signed action; the draft validates every product read-only.
	op := newSignedOperator(t, ctx, env)

	// Putaway session: scan buffer + scan product.
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, op.token, "/putaway/scan-product", map[string]string{
		"product_id":    productID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	// Snapshot outbox count before the failed call.
	outboxBefore := outboxCountForAggregate(t, ctx, env, productID, "putaway")

	// Attempt to place P1 alongside a nonexistent product UUID.
	bogusID := uuid.NewString()
	status, _, _ := draftPlaceToStorageBinRaw(t, env, op.token, []string{productID.String(), bogusID}, fixture.StorageBinID.String())
	require.Equal(t, http.StatusNotFound, status)

	// P1 must still be RECEIVED — nothing was signed, nothing was written.
	requireProductStatus(t, ctx, env, productID, "RECEIVED")

	// No putaway outbox event and no putaway warehouse_action row for P1.
	outboxAfter := outboxCountForAggregate(t, ctx, env, productID, "putaway")
	require.Equal(t, outboxBefore, outboxAfter, "failed scan-storage-bin draft must not create putaway events")

	var putawayActions int
	err := env.db.QueryRow(ctx, `
		SELECT count(*) FROM public.warehouse_actions
		WHERE item_ids @> ARRAY[$1::uuid] AND action_type = 2`, productID).Scan(&putawayActions)
	require.NoError(t, err)
	require.Zero(t, putawayActions, "failed draft must not create a putaway action for the valid product")
}
