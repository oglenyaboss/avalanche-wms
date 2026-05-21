//go:build e2e

package e2e

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const statusShipped uint8 = 4

type scanTTNData struct {
	ShipmentID uuid.UUID `json:"shipment_id"`
}

type scanGateCargoplaceData struct {
	CargoplaceID uuid.UUID `json:"cargoplace_id"`
	Status       string    `json:"status"`
}

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
	AllocatedOrders   int `json:"allocated_orders"`
	AllocatedProducts int `json:"allocated_products"`
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

func TestOutboundFlow_EndToEnd(t *testing.T) {
	// 1. Create the test context and use the e2e environment prepared by TestMain.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)

	// 2. Log in to WMS and load the dedicated seeded order, shipment, bins, SKU, and other fixture data.
	token := login(t, env)
	fixture := loadSeedFixture(t, ctx, env)
	requireOrderStatus(t, ctx, env, fixture.OrderID, "NEW")
	requireDispatchStatus(t, ctx, env, fixture.DispatchID, "SCHEDULED")

	// 3. Scan the seeded cargoplace at the receiving table so it moves to TABLE_IN_PROGRESS.
	postJSON[map[string]any](t, env, token, "/receiving/table/scan-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, nil)

	// 4. Scan the box, SKU barcode, and QR code; scan-qr creates the product that flows through this test.
	var box scanBoxData
	postJSON(t, env, token, "/receiving/table/scan-box", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_barcode":   fixture.BoxBarcode,
	}, &box)
	require.Equal(t, "OPEN", box.Status)

	var sku scanSKUData
	postJSON(t, env, token, "/receiving/table/scan-sku", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"barcode":       fixture.Barcode,
	}, &sku)
	require.Equal(t, fixture.SKUID, sku.SKUID)

	var scannedProduct scanQRData
	postJSON(t, env, token, "/receiving/table/scan-qr", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"box_id":        box.BoxID.String(),
		"sku_id":        sku.SKUID.String(),
		"qr_code":       fixture.QRCode,
	}, &scannedProduct)
	require.Equal(t, "RECEIVED", scannedProduct.Status)
	requireProductStatus(t, ctx, env, scannedProduct.ProductID, "RECEIVED")

	// 5. Close the box, move the product into the receiving buffer, close the cargoplace, and wait for receiving to commit on-chain.
	postJSON[map[string]any](t, env, token, "/receiving/table/close-box", map[string]string{
		"box_id": box.BoxID.String(),
	}, nil)

	postJSON[map[string]any](t, env, token, "/receiving/table/scan-buffer", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	var closeCP closeCargoplaceData
	postJSON(t, env, token, "/receiving/table/close-cargoplace", map[string]string{
		"cargoplace_id": fixture.CargoplaceID.String(),
	}, &closeCP)
	require.Equal(t, "TABLE_CLOSED", closeCP.Status)
	require.Equal(t, 1, closeCP.OutboxEventsCreated)
	requireOutboxCount(t, ctx, env, "receiving", 1)

	receivingEventID := eventIDForAggregate(t, ctx, env, scannedProduct.ProductID, "receiving")
	waitForOnchainCommitted(t, ctx, env, receivingEventID, "receiving")

	// 6. Run putaway: scan the product from the receiving buffer, place it into the storage bin, and wait for putaway to commit on-chain.
	postJSON[map[string]any](t, env, token, "/putaway/scan-buffer", map[string]string{
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)
	postJSON[map[string]any](t, env, token, "/putaway/scan-product", map[string]string{
		"product_id":    scannedProduct.ProductID.String(),
		"buffer_bin_id": fixture.ReceivingBinID.String(),
	}, nil)

	var putaway scanStorageBinData
	postJSON(t, env, token, "/putaway/scan-storage-bin", map[string]any{
		"product_ids":    []string{scannedProduct.ProductID.String()},
		"storage_bin_id": fixture.StorageBinID.String(),
	}, &putaway)
	require.Equal(t, 1, putaway.ProductsPlaced)
	require.Equal(t, 1, putaway.OutboxEventsCreated)
	requireProductStatus(t, ctx, env, scannedProduct.ProductID, "STORED")
	requireOutboxCount(t, ctx, env, "putaway", 1)

	putawayEventID := eventIDForAggregate(t, ctx, env, scannedProduct.ProductID, "putaway")
	waitForOnchainCommitted(t, ctx, env, putawayEventID, "putaway")

	// 7. Allocate the dedicated seeded order and confirm the system only assigns the product stored by this test.
	var allocated allocateData
	postJSON(t, env, token, "/assembly/allocate", map[string]string{
		"destination_id": fixture.DestinationID.String(),
	}, &allocated)
	require.Equal(t, 1, allocated.AllocatedOrders)
	require.Equal(t, 1, allocated.AllocatedProducts)
	requireOrderStatus(t, ctx, env, fixture.OrderID, "ALLOCATED")

	var tasks taskListData
	getJSON(t, env, token, "/assembly/tasks?destination_id="+url.QueryEscape(fixture.DestinationID.String()), &tasks)
	productIDs := productIDsFromTasks(tasks.Tasks)
	require.Equal(t, []string{scannedProduct.ProductID.String()}, productIDs)

	// 8. Pick the product: complete the assembly task, create the picking outbox event, and wait for picking to commit on-chain.
	eventIDs := []uuid.UUID{receivingEventID, putawayEventID}
	expectedAggregates := []string{"receiving", "putaway"}
	for i, productID := range productIDs {
		parsedProductID, err := uuid.Parse(productID)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsedProductID, "ALLOCATED")
		requireTaskStatus(t, ctx, env, parsedProductID, "PENDING")

		var picked pickData
		postJSON(t, env, token, "/assembly/pick", map[string]string{
			"product_id": productID,
		}, &picked)
		require.Equal(t, productID, picked.ProductID)
		require.Equal(t, i+1, picked.CartSize)
		requireProductStatus(t, ctx, env, parsedProductID, "ASSEMBLED")
		requireTaskStatus(t, ctx, env, parsedProductID, "DONE")

		pickingEventID := eventIDForAggregate(t, ctx, env, parsedProductID, "picking")
		waitForOnchainCommitted(t, ctx, env, pickingEventID, "picking")
		eventIDs = append(eventIDs, pickingEventID)
		expectedAggregates = append(expectedAggregates, "picking")
	}
	requireOutboxCount(t, ctx, env, "picking", len(productIDs))

	// 9. Move the product from the picking cart into the shipping buffer so the order becomes ASSEMBLED.
	var placed scanShippingBufferData
	postJSON(t, env, token, "/assembly/scan-shipping-buffer", map[string]string{
		"buffer_bin_id": fixture.ShippingBinID.String(),
	}, &placed)
	require.Equal(t, len(productIDs), placed.ProductsPlaced)
	require.Equal(t, 1, placed.OrdersAssembled)
	requireOrderStatus(t, ctx, env, fixture.OrderID, "ASSEMBLED")
	for _, productID := range productIDs {
		parsedProductID, err := uuid.Parse(productID)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsedProductID, "READY_TO_SHIP")
	}

	// 10. Scan the shipping buffer and driver, ship the product, wait for shipping to commit on-chain, and verify the contract status is Shipped.
	postJSON[map[string]any](t, env, token, "/shipping/scan-buffer", map[string]string{
		"buffer_bin_id": fixture.ShippingBinID.String(),
	}, nil)

	var driver scanDriverData
	postJSON(t, env, token, "/shipping/scan-driver", map[string]string{
		"dispatch_code": fixture.DispatchCode,
	}, &driver)
	require.Equal(t, fixture.DispatchID, driver.DispatchID)
	require.Equal(t, "AT_GATE", driver.Status)
	requireDispatchStatus(t, ctx, env, fixture.DispatchID, "AT_GATE")

	var shipped shipData
	postJSON(t, env, token, "/shipping/ship", map[string]any{
		"buffer_bin_id": fixture.ShippingBinID.String(),
		"dispatch_id":   fixture.DispatchID.String(),
		"product_ids":   productIDs,
	}, &shipped)
	require.Equal(t, len(productIDs), shipped.ProductsShipped)
	require.Equal(t, len(productIDs), shipped.OutboxEventsCreated)
	require.Equal(t, 1, shipped.OrdersCompleted)
	require.True(t, shipped.DispatchDeparted)
	require.Equal(t, 0, shipped.BufferRemaining)
	requireOrderStatus(t, ctx, env, fixture.OrderID, "SHIPPED")
	requireDispatchStatus(t, ctx, env, fixture.DispatchID, "DEPARTED")
	requireOutboxCount(t, ctx, env, "shipping", len(productIDs))

	for _, productID := range productIDs {
		parsedProductID, err := uuid.Parse(productID)
		require.NoError(t, err)
		requireProductStatus(t, ctx, env, parsedProductID, "SHIPPED")

		shippingEventID := eventIDForAggregate(t, ctx, env, parsedProductID, "shipping")
		waitForOnchainCommitted(t, ctx, env, shippingEventID, "shipping")
		eventIDs = append(eventIDs, shippingEventID)
		expectedAggregates = append(expectedAggregates, "shipping")

		status := callItemStatus(t, ctx, env, parsedProductID)
		require.Equal(t, statusShipped, status)
	}

	// 11. Finally, verify the business order of the four on-chain events and ensure each tx_hash is present.
	requireFSMSequence(t, ctx, env, eventIDs, expectedAggregates)
}

func productIDsFromTasks(tasks []taskData) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ProductID)
	}
	return ids
}
