import { describe, expect, it } from "vitest";

import { api, unwrap } from "@/lib/api/client";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";

import { seedIds } from "../data/fixture-kit";
import { db } from "../db";
import { loginAs, setupMockApi, waitForChain } from "../testing";

setupMockApi();

const { bins, destinations, dispatches, orders } = seedIds;
const e2eBarcode = "4600000099999";
const qr = "QR-PATH-0001";

const operationParts = async (operation_id: string) =>
  (await unwrap(api.GET("/signed-actions/operations/{operation_id}", { params: { path: { operation_id } } }))).parts;
const productRow = () => db().products.find((row) => row.qr_code === qr);
const orderStatus = () => db().orders.find((row) => row.order_id === orders["ORD-2026-0502"])?.status;

describe("полный путь товара", () => {
  it("КПП → стол → раскладка → сборка → отгрузка, все операции APPLIED", async () => {
    await loginAs("operator");

    const ttn = await unwrap(api.POST("/receiving/gate/scan-ttn", { body: { ttn_code: "ТТН-2026-GATE-001" } }));
    expect(ttn).toMatchObject({ status: "GATE_IN_PROGRESS", total_cargoplaces: 2, received_cargoplaces: 0 });
    const cargoplaceId = ttn.cargoplaces.find((row) => row.cargoplace_code === "CP-GATE-001")!.cargoplace_id;
    await unwrap(api.POST("/receiving/gate/scan-cargoplace", { body: { shipment_id: ttn.shipment_id, cargoplace_code: "CP-GATE-001" } }));
    const last = await unwrap(api.POST("/receiving/gate/scan-cargoplace", { body: { shipment_id: ttn.shipment_id, cargoplace_code: "CP-GATE-002" } }));
    expect(last.progress).toEqual({ total: 2, received: 2, remaining: 0, not_received: 0 });

    await unwrap(api.POST("/receiving/table/scan-cargoplace", { body: { cargoplace_id: cargoplaceId } }));
    const box = await unwrap(api.POST("/receiving/table/scan-box", { body: { cargoplace_id: cargoplaceId, box_barcode: "BOX-PATH-001" } }));
    const sku = await unwrap(api.POST("/receiving/table/scan-sku", { body: { cargoplace_id: cargoplaceId, box_id: box.box_id, barcode: e2eBarcode } }));
    expect(sku.sku_name).toBe("E2E Seed Outbound SKU");
    const product = await unwrap(api.POST("/receiving/table/scan-qr", { body: { cargoplace_id: cargoplaceId, box_id: box.box_id, sku_id: sku.sku_id, qr_code: qr } }));
    const productId = product.product_id;
    await unwrap(api.POST("/receiving/table/close-box", { body: { box_id: box.box_id } }));
    const buffered = await unwrap(api.POST("/receiving/table/scan-buffer", { body: { cargoplace_id: cargoplaceId, buffer_bin_id: bins["BUFFER-01"] } }));
    expect(buffered).toEqual({ buffer_bin_id: bins["BUFFER-01"], buffer_code: "BUFFER-01", products_placed: 1 });
    const receive = await signAndSubmit(
      await unwrap(api.POST("/receiving/table/close-cargoplace/draft", { body: { cargoplace_id: cargoplaceId } })),
      (body: SignedSubmit) => unwrap(api.POST("/receiving/table/close-cargoplace/submit", { body })),
    );
    expect(receive).toMatchObject({ status: "TABLE_CLOSED", summary: { products_received: 1, shortage: 0 } });
    await waitForChain();

    await expect(unwrap(api.POST("/assembly/allocate", { body: { destination_id: destinations["SHOP-5"] } }))).resolves.toMatchObject({
      insufficient_orders: [expect.objectContaining({ order_id: orders["ORD-2026-0502"] })],
    });
    await unwrap(api.POST("/putaway/scan-buffer", { body: { buffer_bin_id: bins["BUFFER-01"] } }));
    await unwrap(api.POST("/putaway/scan-product", { body: { product_id: productId, buffer_bin_id: bins["BUFFER-01"] } }));
    const putaway = await signAndSubmit(
      await unwrap(api.POST("/putaway/scan-storage-bin/draft", { body: { product_ids: [productId], storage_bin_id: bins["A-01-01"] } })),
      (body: SignedSubmit) => unwrap(api.POST("/putaway/scan-storage-bin/submit", { body })),
    );
    await waitForChain();
    expect(productRow()).toMatchObject({ status: "STORED", bin_id: bins["A-01-01"] });

    const allocation = await unwrap(api.POST("/assembly/allocate", { body: { destination_id: destinations["SHOP-5"] } }));
    expect(allocation.insufficient_orders).toEqual([]);
    expect(productRow()).toMatchObject({ status: "ALLOCATED", order_id: orders["ORD-2026-0502"] });
    const { tasks } = await unwrap(api.GET("/assembly/tasks", { params: { query: { destination_id: destinations["SHOP-5"] } } }));
    expect(tasks.find((task) => task.qr_code === qr)).toMatchObject({ product_id: productId, from_bin_code: "A-01-01", order_no: "ORD-2026-0502" });
    await expect(unwrap(api.POST("/assembly/pick", { body: { product_id: productId } }))).resolves.toEqual({ product_id: productId, cart_size: 2 });
    const pick = await signAndSubmit(
      await unwrap(api.POST("/assembly/scan-shipping-buffer/draft", { body: { buffer_bin_id: bins["BIN-SHIP-5"] } })),
      (body: SignedSubmit) => unwrap(api.POST("/assembly/scan-shipping-buffer/submit", { body })),
    );
    expect(pick).toMatchObject({ products_placed: 1, orders_assembled: 1 });
    expect(orderStatus()).toBe("ASSEMBLED");

    const driver = await unwrap(api.POST("/shipping/scan-driver", { body: { dispatch_code: "DSP-2026-0421-001" } }));
    expect(driver).toMatchObject({ dispatch_id: dispatches["DSP-2026-0421-001"], status: "AT_GATE" });
    const shipDraft = () =>
      unwrap(api.POST("/shipping/ship/draft", { body: { buffer_bin_id: bins["BIN-SHIP-5"], dispatch_id: dispatches["DSP-2026-0421-001"], product_ids: [productId] } }));
    await expect(shipDraft()).rejects.toMatchObject({
      status: 409,
      code: "PICKING_NOT_CONFIRMED",
      message: "Сборка части товаров (1 шт.) ещё не подтверждена в цепи. Повторите отгрузку чуть позже",
    });
    await waitForChain();
    const scanned = await unwrap(api.POST("/shipping/scan-buffer", { body: { buffer_bin_id: bins["BIN-SHIP-5"] } }));
    expect(scanned.products).toContainEqual({ product_id: productId, qr_code: qr, sku_name: "E2E Seed Outbound SKU", order_external_no: "ORD-2026-0502" });
    const ship = await signAndSubmit(await shipDraft(), (body: SignedSubmit) => unwrap(api.POST("/shipping/ship/submit", { body })));
    expect(ship).toMatchObject({ products_shipped: 1, orders_completed: 1 });
    await waitForChain();

    expect(productRow()).toMatchObject({ status: "SHIPPED", order_id: orders["ORD-2026-0502"] });
    expect(orderStatus()).toBe("SHIPPED");
    for (const operationId of [receive.operation_id, putaway.operation_id, pick.operation_id, ship.operation_id]) {
      expect(await operationParts(operationId)).toEqual([expect.objectContaining({ state: "APPLIED", reject_reason: null })]);
    }
  });
});
