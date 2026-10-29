import { itemId } from "@/lib/signing/adr";

import type { AssemblyTaskRow, ProductRow, PutawayRow, ReceivingTableRow, ShippingRow } from "../../db";
import { ago, combineFixtures, idGroup, mockId, seedIds, signedActionFixture, type Fixture } from "../fixture-kit";
import { shippingIds } from "./shipping";

const p = {
  "QR-HIS-0001": mockId(idGroup.product, 601),
  "QR-HIS-0002": mockId(idGroup.product, 602),
  "QR-HIS-0003": mockId(idGroup.product, 603),
  "QR-HIS-0004": mockId(idGroup.product, 604),
  "QR-HIS-0005": mockId(idGroup.product, 605),
  "QR-HIS-0006": mockId(idGroup.product, 606),
  "QR-HIS-0007": mockId(idGroup.product, 607),
} as const;

export const historyIds = {
  sku: mockId(idGroup.sku, 601),
  cargoplace: mockId(idGroup.cargoplace, 601),
  box: mockId(idGroup.box, 601),
  order: mockId(idGroup.order, 601),
  products: p,
  action: (n: number) => mockId(idGroup.action, n),
};

const shipment = mockId(idGroup.shipment, 601);
const operation = mockId(idGroup.operation, 601);
const op = seedIds.users.operator;
const bins = seedIds.bins;

const sortedByItemId = (ids: readonly string[]) => [...ids].sort((a, b) => (itemId(a) < itemId(b) ? -1 : itemId(a) > itemId(b) ? 1 : 0));

const historyProduct = (qr: keyof typeof p, binId: string | null, orderId: string | null, status: ProductRow["status"], boxId: string | null, updatedAgo: number): ProductRow => ({
  product_id: p[qr],
  sku_id: historyIds.sku,
  shipment_id: shipment,
  cargoplace_id: historyIds.cargoplace,
  box_id: boxId,
  qr_code: qr,
  bin_id: binId,
  order_id: orderId,
  status,
  created_at: ago(4400),
  updated_at: ago(updatedAgo),
});

const scanQrRow = (qr: keyof typeof p, eventN: number): ReceivingTableRow => ({
  event_id: mockId(idGroup.event, eventN),
  cargoplace_id: historyIds.cargoplace,
  box_id: historyIds.box,
  operator_id: op,
  action: "SCAN_QR",
  box_barcode: null,
  sku_id: historyIds.sku,
  qr_code: qr,
  product_id: p[qr],
  buffer_bin_id: null,
  operation_id: null,
  occurred_at: ago(4330),
  created_at: ago(4330),
});

const closeCargoRow = (created: number): ReceivingTableRow => ({
  event_id: mockId(idGroup.event, 614),
  cargoplace_id: historyIds.cargoplace,
  box_id: null,
  operator_id: op,
  action: "CLOSE_CARGO",
  box_barcode: null,
  sku_id: null,
  qr_code: null,
  product_id: null,
  buffer_bin_id: null,
  operation_id: operation,
  occurred_at: ago(created),
  created_at: ago(created),
});

const putawayRow = (actionN: number, eventN: number, productId: string, fromBinId: string | null, binId: string, minutesAgo: number): PutawayRow => ({
  event_id: mockId(idGroup.event, eventN),
  product_id: productId,
  from_bin_id: fromBinId,
  bin_id: binId,
  operator_id: op,
  action_id: historyIds.action(actionN),
  occurred_at: ago(minutesAgo),
  created_at: ago(minutesAgo),
});

const assemblyTaskRow = (): AssemblyTaskRow => ({
  event_id: mockId(idGroup.event, 631),
  order_id: historyIds.order,
  product_id: p["QR-HIS-0001"],
  sku_id: historyIds.sku,
  from_bin_id: bins["A-01-01"],
  section: "A",
  destination_id: seedIds.destinations["SHOP-5"],
  status: "DONE",
  operator_id: op,
  action_id: historyIds.action(603),
  occurred_at: ago(1445),
  unloaded_at: ago(1440),
  created_at: ago(1500),
  updated_at: ago(1440),
});

const shippingRow = (): ShippingRow => ({
  event_id: mockId(idGroup.event, 641),
  product_id: p["QR-HIS-0001"],
  operator_id: op,
  dispatch_id: shippingIds.dispatches["DSP-SHP-003"],
  action_id: historyIds.action(604),
  shipped_at: ago(10),
  occurred_at: ago(10),
  created_at: ago(10),
});

export const historyFixtures = (): Fixture =>
  combineFixtures(
    {
      skus: [{ sku_id: historyIds.sku, name: "Куртка Columbia Watertight", description: null, volume: 12 }],
      inbound_shipments: [{ shipment_id: shipment, warehouse_id: 1, ttn_code: "ТТН-2026-HIS-001", status: "GATE_CLOSED", created_at: ago(4400), updated_at: ago(4400) }],
      cargoplaces: [
        { cargoplace_id: historyIds.cargoplace, shipment_id: shipment, cargoplace_code: "CP-HIS-001", status: "TABLE_CLOSED", received_at_gate_at: ago(4400), created_at: ago(4400), updated_at: ago(4400) },
      ],
      boxes: [{ box_id: historyIds.box, cargoplace_id: historyIds.cargoplace, box_barcode: "BOX-HIS-001", status: "CLOSED", created_at: ago(4400), updated_at: ago(4400) }],
      orders: [
        {
          order_id: historyIds.order,
          external_order_no: "ORD-HIS-001",
          customer_id: seedIds.users.customer,
          warehouse_id: 1,
          destination_id: seedIds.destinations["SHOP-5"],
          status: "SHIPPED",
          created_at: ago(4400),
          updated_at: ago(4400),
        },
      ],
      order_lines: [{ order_id: historyIds.order, sku_id: historyIds.sku, qty: 1 }],
      products: [
        historyProduct("QR-HIS-0001", bins["BIN-SHIP-5"], historyIds.order, "SHIPPED", historyIds.box, 2),
        historyProduct("QR-HIS-0002", bins["A-02-01"], null, "STORED", historyIds.box, 3),
        historyProduct("QR-HIS-0003", bins["A-01-01"], null, "STORED", historyIds.box, 4),
        historyProduct("QR-HIS-0004", bins["A-01-02"], null, "STORED", null, 5),
        historyProduct("QR-HIS-0005", bins["B-01-01"], null, "STORED", null, 6),
        historyProduct("QR-HIS-0006", bins["B-01-02"], null, "STORED", null, 7),
        historyProduct("QR-HIS-0007", null, null, "SHIPPED", null, 8),
      ],
      receiving_table: [scanQrRow("QR-HIS-0001", 611), scanQrRow("QR-HIS-0002", 612), scanQrRow("QR-HIS-0003", 613), closeCargoRow(4320)],
      putaways: [
        putawayRow(602, 621, p["QR-HIS-0001"], bins["BUFFER-01"], bins["A-01-01"], 2880),
        putawayRow(605, 622, p["QR-HIS-0002"], bins["BUFFER-01"], bins["A-01-02"], 2940),
        putawayRow(606, 623, p["QR-HIS-0002"], bins["A-01-02"], bins["A-02-01"], 30),
        putawayRow(608, 624, p["QR-HIS-0004"], bins["BUFFER-01"], bins["A-01-02"], 1500),
        putawayRow(608, 625, p["QR-HIS-0005"], bins["BUFFER-01"], bins["B-01-01"], 1500),
      ],
      assembly_tasks: [assemblyTaskRow()],
      shippings: [shippingRow()],
    },
    signedActionFixture({ n: 601, actionType: 1, items: sortedByItemId([p["QR-HIS-0001"], p["QR-HIS-0002"], p["QR-HIS-0003"]]), outcome: { status: "COMMITTED" }, minutesAgo: 4320 }),
    signedActionFixture({ n: 602, actionType: 2, items: [p["QR-HIS-0001"]], outcome: { status: "COMMITTED" }, minutesAgo: 2880 }),
    signedActionFixture({ n: 603, actionType: 3, items: [p["QR-HIS-0001"]], outcome: { status: "COMMITTED" }, minutesAgo: 1440 }),
    signedActionFixture({ n: 604, actionType: 4, items: [p["QR-HIS-0001"]], outcome: { status: "SENT" }, minutesAgo: 10 }),
    signedActionFixture({ n: 605, actionType: 2, items: [p["QR-HIS-0002"]], outcome: { status: "COMMITTED" }, minutesAgo: 2940 }),
    signedActionFixture({ n: 606, actionType: 2, items: [p["QR-HIS-0002"]], outcome: null, minutesAgo: 30 }),
    signedActionFixture({ n: 607, actionType: 2, items: [p["QR-HIS-0003"]], outcome: { status: "FAILED", rejectReason: 12 }, minutesAgo: 2000 }),
    signedActionFixture({ n: 608, actionType: 2, items: [p["QR-HIS-0004"], p["QR-HIS-0005"]], outcome: { status: "COMMITTED" }, minutesAgo: 1500 }),
    signedActionFixture({ n: 609, actionType: 1, items: [p["QR-HIS-0007"]], outcome: { status: "COMMITTED" }, minutesAgo: 28800 }),
    signedActionFixture({ n: 610, actionType: 3, items: [p["QR-HIS-0007"]], outcome: { status: "COMMITTED" }, minutesAgo: 19410 }),
    signedActionFixture({ n: 611, actionType: 2, items: [p["QR-HIS-0007"]], outcome: { status: "COMMITTED" }, minutesAgo: 19470 }),
  );
