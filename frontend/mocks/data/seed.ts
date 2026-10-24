import { bytesToHex, concatHex, keccak256 } from "viem";

import { fromBase64url } from "@/lib/signing/base64url";

import type { Db, ProductRow, ProductStatus } from "../db";
import { ago, idGroup, later, mergeFixtures, mockId, seedIds, type Tables } from "./fixture-kit";
import { assemblyFixtures } from "./fixtures/assembly";
import { historyFixtures } from "./fixtures/history";
import { passkeysFixtures } from "./fixtures/passkeys";
import { putawayFixtures } from "./fixtures/putaway";
import { receivingFixtures } from "./fixtures/receiving";
import { shippingFixtures } from "./fixtures/shipping";
import { seededKeys } from "./keys";

export const dbVersion = 1;

const warehouseId = 1;
const { users, bins, skus, shipments, cargoplaces, boxes, orders, destinations } = seedIds;

function seededProduct(
  qr: string,
  sku: string,
  shipment: string,
  cargoplace: string,
  box: string | null,
  bin: string | null,
  order: string | null,
  status: ProductStatus,
  n: number,
): ProductRow {
  return {
    product_id: mockId(idGroup.product, n),
    sku_id: sku,
    shipment_id: shipment,
    cargoplace_id: cargoplace,
    box_id: box,
    qr_code: qr,
    bin_id: bin,
    order_id: order,
    status,
    created_at: ago(180 - n),
    updated_at: ago(180 - n),
  };
}

function base(): Tables {
  const created = ago(240);
  const p00142 = (qr: string, sku: string, cp: "CP-001" | "CP-002", box: keyof typeof boxes, bin: keyof typeof bins | null, order: keyof typeof orders | null, status: ProductStatus, n: number) =>
    seededProduct(qr, sku, shipments["ТТН-2026-00142"], cargoplaces[cp], boxes[box], bin ? bins[bin] : null, order ? orders[order] : null, status, n);

  return {
    users: [
      { user_id: users.operator, username: "operator", password: "operator", role: "OPERATOR", is_active: true, org_id: seedIds.org, created_at: created },
      { user_id: users.admin, username: "admin", password: "admin", role: "ADMIN", is_active: true, org_id: seedIds.org, created_at: created },
      { user_id: users.auditor, username: "auditor", password: "auditor", role: "AUDITOR", is_active: true, org_id: null, created_at: created },
      { user_id: users.customer, username: "customer", password: "customer", role: "CUSTOMER", is_active: true, org_id: null, created_at: created },
      { user_id: users.operator2, username: "operator2", password: "operator2", role: "OPERATOR", is_active: true, org_id: seedIds.org, created_at: created },
    ],
    organizations: [{ org_id: seedIds.org, name: "Демо-склад", created_at: created }],
    warehouses: [{ warehouse_id: warehouseId, name: "Склад Москва-Север", address: "г. Москва, ул. Складская, 15", contact: "+7 (495) 100-10-10" }],
    destinations: [
      { destination_id: destinations["SHOP-5"], code: "SHOP-5", name: "Магазин №5 Маросейка", address: "г. Москва, ул. Маросейка, 5", warehouse_id: warehouseId },
      { destination_id: destinations["SHOP-7"], code: "SHOP-7", name: "Магазин №7 Петровка", address: "г. Москва, ул. Петровка, 7", warehouse_id: warehouseId },
    ],
    bins: [
      { bin_id: bins["A-01-01"], warehouse_id: warehouseId, code: "A-01-01", section: "A", volume: 120, destination_id: null },
      { bin_id: bins["A-01-02"], warehouse_id: warehouseId, code: "A-01-02", section: "A", volume: 120, destination_id: null },
      { bin_id: bins["A-02-01"], warehouse_id: warehouseId, code: "A-02-01", section: "A", volume: 110, destination_id: null },
      { bin_id: bins["B-01-01"], warehouse_id: warehouseId, code: "B-01-01", section: "B", volume: 130, destination_id: null },
      { bin_id: bins["B-01-02"], warehouse_id: warehouseId, code: "B-01-02", section: "B", volume: 130, destination_id: null },
      { bin_id: bins["BUFFER-01"], warehouse_id: warehouseId, code: "BUFFER-01", section: "BUFFER", volume: 200, destination_id: null },
      { bin_id: bins["BIN-SHIP-5"], warehouse_id: warehouseId, code: "BIN-SHIP-5", section: "SHIPPING_BUFFER", volume: 180, destination_id: destinations["SHOP-5"] },
      { bin_id: bins["BIN-SHIP-7"], warehouse_id: warehouseId, code: "BIN-SHIP-7", section: "SHIPPING_BUFFER", volume: 180, destination_id: destinations["SHOP-7"] },
    ],
    skus: [
      { sku_id: skus.nike, name: "Кроссовки Nike Air Max 90", description: "Повседневные кроссовки, мужская линейка", volume: 9.5 },
      { sku_id: skus.adidas, name: "Футболка Adidas Originals", description: "Базовая хлопковая футболка", volume: 1.2 },
      { sku_id: skus.puma, name: "Рюкзак Puma Core", description: "Городской рюкзак 22 литра", volume: 7 },
      { sku_id: skus.tnf, name: "Куртка The North Face", description: "Демисезонная куртка", volume: 12 },
      { sku_id: skus.e2e, name: "E2E Seed Outbound SKU", description: "Dedicated SKU for seeded outbound e2e flow", volume: 1 },
    ],
    sku_barcodes: [
      { sku_id: skus.nike, barcode: "4600000000011" },
      { sku_id: skus.nike, barcode: "4600000000028" },
      { sku_id: skus.adidas, barcode: "4600000000035" },
      { sku_id: skus.puma, barcode: "4600000000042" },
      { sku_id: skus.tnf, barcode: "4600000000059" },
      { sku_id: skus.e2e, barcode: "4600000099999" },
    ],
    inbound_shipments: [
      { shipment_id: shipments["ТТН-2026-00142"], warehouse_id: warehouseId, ttn_code: "ТТН-2026-00142", status: "RECEIVED", created_at: created, updated_at: created },
      { shipment_id: shipments["ТТН-2026-TABLE-001"], warehouse_id: warehouseId, ttn_code: "ТТН-2026-TABLE-001", status: "GATE_IN_PROGRESS", created_at: created, updated_at: created },
      { shipment_id: shipments["ТТН-2026-TABLE-002"], warehouse_id: warehouseId, ttn_code: "ТТН-2026-TABLE-002", status: "GATE_CLOSED", created_at: created, updated_at: created },
      { shipment_id: shipments["ТТН-2026-GATE-001"], warehouse_id: warehouseId, ttn_code: "ТТН-2026-GATE-001", status: "CREATED", created_at: created, updated_at: created },
      { shipment_id: shipments["ТТН-2026-GATE-002"], warehouse_id: warehouseId, ttn_code: "ТТН-2026-GATE-002", status: "CREATED", created_at: created, updated_at: created },
    ],
    cargoplaces: [
      { cargoplace_id: cargoplaces["CP-001"], shipment_id: shipments["ТТН-2026-00142"], cargoplace_code: "CP-001", status: "RECEIVED", received_at_gate_at: ago(120), created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-002"], shipment_id: shipments["ТТН-2026-00142"], cargoplace_code: "CP-002", status: "RECEIVED", received_at_gate_at: ago(120), created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-TABLE-001"], shipment_id: shipments["ТТН-2026-TABLE-001"], cargoplace_code: "CP-TABLE-001", status: "RECEIVED_AT_GATE", received_at_gate_at: ago(15), created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-TABLE-002"], shipment_id: shipments["ТТН-2026-TABLE-002"], cargoplace_code: "CP-TABLE-002", status: "TABLE_IN_PROGRESS", received_at_gate_at: ago(45), created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-GATE-001"], shipment_id: shipments["ТТН-2026-GATE-001"], cargoplace_code: "CP-GATE-001", status: "EXPECTED", received_at_gate_at: null, created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-GATE-002"], shipment_id: shipments["ТТН-2026-GATE-001"], cargoplace_code: "CP-GATE-002", status: "EXPECTED", received_at_gate_at: null, created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-GATE-003"], shipment_id: shipments["ТТН-2026-GATE-002"], cargoplace_code: "CP-GATE-003", status: "EXPECTED", received_at_gate_at: null, created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-GATE-004"], shipment_id: shipments["ТТН-2026-GATE-002"], cargoplace_code: "CP-GATE-004", status: "EXPECTED", received_at_gate_at: null, created_at: created, updated_at: created },
    ],
    boxes: [
      { box_id: boxes["BOX-001"], cargoplace_id: cargoplaces["CP-001"], box_barcode: "BOX-001", status: "OPEN", created_at: created, updated_at: created },
      { box_id: boxes["BOX-002"], cargoplace_id: cargoplaces["CP-001"], box_barcode: "BOX-002", status: "OPEN", created_at: created, updated_at: created },
      { box_id: boxes["BOX-003"], cargoplace_id: cargoplaces["CP-002"], box_barcode: "BOX-003", status: "OPEN", created_at: created, updated_at: created },
      { box_id: boxes["BOX-TABLE-201"], cargoplace_id: cargoplaces["CP-TABLE-002"], box_barcode: "BOX-TABLE-201", status: "CLOSED", created_at: created, updated_at: created },
    ],
    expected_cargoplace_skus: [
      { cargoplace_id: cargoplaces["CP-001"], sku_id: skus.nike, expected_qty: 4 },
      { cargoplace_id: cargoplaces["CP-001"], sku_id: skus.adidas, expected_qty: 3 },
      { cargoplace_id: cargoplaces["CP-002"], sku_id: skus.puma, expected_qty: 2 },
      { cargoplace_id: cargoplaces["CP-TABLE-001"], sku_id: skus.nike, expected_qty: 2 },
      { cargoplace_id: cargoplaces["CP-TABLE-001"], sku_id: skus.adidas, expected_qty: 1 },
      { cargoplace_id: cargoplaces["CP-TABLE-001"], sku_id: skus.e2e, expected_qty: 1 },
      { cargoplace_id: cargoplaces["CP-TABLE-002"], sku_id: skus.nike, expected_qty: 2 },
      { cargoplace_id: cargoplaces["CP-TABLE-002"], sku_id: skus.adidas, expected_qty: 2 },
    ],
    orders: [
      { order_id: orders["ORD-2026-0042"], external_order_no: "ORD-2026-0042", customer_id: users.customer, warehouse_id: warehouseId, destination_id: destinations["SHOP-5"], status: "ALLOCATED", created_at: ago(200), updated_at: ago(200) },
      { order_id: orders["ORD-2026-0043"], external_order_no: "ORD-2026-0043", customer_id: users.customer, warehouse_id: warehouseId, destination_id: destinations["SHOP-7"], status: "SHIPPED", created_at: ago(199), updated_at: ago(199) },
      { order_id: orders["ORD-2026-0501"], external_order_no: "ORD-2026-0501", customer_id: users.customer, warehouse_id: warehouseId, destination_id: destinations["SHOP-5"], status: "NEW", created_at: ago(198), updated_at: ago(198) },
      { order_id: orders["ORD-2026-0502"], external_order_no: "ORD-2026-0502", customer_id: users.customer, warehouse_id: warehouseId, destination_id: destinations["SHOP-5"], status: "NEW", created_at: ago(197), updated_at: ago(197) },
    ],
    order_lines: [
      { order_id: orders["ORD-2026-0042"], sku_id: skus.nike, qty: 1 },
      { order_id: orders["ORD-2026-0042"], sku_id: skus.adidas, qty: 1 },
      { order_id: orders["ORD-2026-0043"], sku_id: skus.puma, qty: 1 },
      { order_id: orders["ORD-2026-0043"], sku_id: skus.tnf, qty: 1 },
      { order_id: orders["ORD-2026-0501"], sku_id: skus.nike, qty: 5 },
      { order_id: orders["ORD-2026-0501"], sku_id: skus.adidas, qty: 5 },
      { order_id: orders["ORD-2026-0502"], sku_id: skus.e2e, qty: 1 },
    ],
    products: [
      seededProduct("QR-TABLE-2001", skus.nike, shipments["ТТН-2026-TABLE-002"], cargoplaces["CP-TABLE-002"], boxes["BOX-TABLE-201"], null, null, "RECEIVED", 101),
      seededProduct("QR-TABLE-2002", skus.nike, shipments["ТТН-2026-TABLE-002"], cargoplaces["CP-TABLE-002"], boxes["BOX-TABLE-201"], null, null, "RECEIVED", 102),
      seededProduct("QR-TABLE-2003", skus.adidas, shipments["ТТН-2026-TABLE-002"], cargoplaces["CP-TABLE-002"], boxes["BOX-TABLE-201"], null, null, "RECEIVED", 103),
      p00142("QR-2026-0001", skus.nike, "CP-001", "BOX-001", null, null, "RECEIVED", 1),
      p00142("QR-2026-0002", skus.adidas, "CP-001", "BOX-001", null, null, "RECEIVED", 2),
      p00142("QR-2026-0003", skus.puma, "CP-002", "BOX-003", "A-01-01", null, "STORED", 3),
      p00142("QR-2026-0004", skus.tnf, "CP-002", "BOX-003", "B-01-01", null, "STORED", 4),
      p00142("QR-2026-0005", skus.nike, "CP-001", "BOX-002", "A-01-02", "ORD-2026-0042", "ALLOCATED", 5),
      p00142("QR-2026-0006", skus.adidas, "CP-001", "BOX-002", "A-02-01", "ORD-2026-0042", "ALLOCATED", 6),
      p00142("QR-2026-0007", skus.puma, "CP-002", "BOX-003", "B-01-02", "ORD-2026-0043", "SHIPPED", 7),
      p00142("QR-2026-0008", skus.tnf, "CP-002", "BOX-003", "B-01-02", "ORD-2026-0043", "SHIPPED", 8),
      p00142("QR-2026-0009", skus.nike, "CP-001", "BOX-001", "A-01-01", null, "STORED", 9),
      p00142("QR-2026-0010", skus.nike, "CP-001", "BOX-001", "A-01-01", null, "STORED", 10),
      p00142("QR-2026-0011", skus.nike, "CP-001", "BOX-001", "A-01-02", null, "STORED", 11),
      p00142("QR-2026-0012", skus.nike, "CP-001", "BOX-002", "A-01-02", null, "STORED", 12),
      p00142("QR-2026-0013", skus.nike, "CP-001", "BOX-002", "A-02-01", null, "STORED", 13),
      p00142("QR-2026-0014", skus.adidas, "CP-001", "BOX-001", "A-02-01", null, "STORED", 14),
      p00142("QR-2026-0015", skus.adidas, "CP-001", "BOX-001", "A-02-01", null, "STORED", 15),
      p00142("QR-2026-0016", skus.adidas, "CP-001", "BOX-002", "B-01-01", null, "STORED", 16),
      p00142("QR-2026-0017", skus.adidas, "CP-001", "BOX-002", "B-01-01", null, "STORED", 17),
      p00142("QR-2026-0018", skus.adidas, "CP-001", "BOX-002", "B-01-02", null, "STORED", 18),
    ],
    outbound_dispatches: [
      {
        dispatch_id: seedIds.dispatches["DSP-2026-0421-001"],
        dispatch_code: "DSP-2026-0421-001",
        warehouse_id: warehouseId,
        destination_id: destinations["SHOP-5"],
        vehicle_number: "A123BC777",
        driver_name: "Иван Петров",
        driver_phone: "+7 (999) 555-00-11",
        status: "SCHEDULED",
        scheduled_at: later(24 * 60),
        arrived_at: null,
        departed_at: null,
        created_at: created,
        updated_at: created,
      },
    ],
    receiving_gate: [],
    receiving_table: [],
    putaways: [],
    assembly_tasks: [],
    shippings: [],
    passkey_credentials: seededKeys.map((key) => {
      const qx = bytesToHex(fromBase64url(key.jwk.x!));
      const qy = bytesToHex(fromBase64url(key.jwk.y!));
      const user = key.username === "admin" ? users.admin : users.operator;
      return {
        credential_id: key.credentialId,
        user_id: user,
        org_id: seedIds.org,
        qx,
        qy,
        key_id: keccak256(concatHex([qx, qy])),
        role: key.username === "admin" ? "ADMIN" : "OPERATOR",
        status: "ACTIVE",
        created_at: created,
        updated_at: created,
      };
    }),
    key_actions: [],
    registration_sessions: [],
    warehouse_actions: [],
    outbox_events: [],
    onchain_events: [],
  };
}

export function seed(): Db {
  const tables = base();
  mergeFixtures(tables, receivingFixtures(), putawayFixtures(), assemblyFixtures(), shippingFixtures(), passkeysFixtures(), historyFixtures());
  return { version: dbVersion, ...tables };
}
