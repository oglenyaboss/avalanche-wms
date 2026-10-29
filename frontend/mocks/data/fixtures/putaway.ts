import type { ProductRow } from "../../db";
import { ago, combineFixtures, idGroup, mockId, seedIds, signedActionFixture, type Fixture } from "../fixture-kit";

export const putawayIds = {
  shipment: mockId(idGroup.shipment, 201),
  cargoplaces: {
    "CP-PUT-001": mockId(idGroup.cargoplace, 201),
    "CP-PUT-002": mockId(idGroup.cargoplace, 202),
    "CP-PUT-003": mockId(idGroup.cargoplace, 203),
  },
  boxes: {
    "BOX-PUT-001": mockId(idGroup.box, 201),
    "BOX-PUT-002": mockId(idGroup.box, 202),
    "BOX-PUT-003": mockId(idGroup.box, 203),
  },
  products: {
    "QR-PUT-0001": mockId(idGroup.product, 201),
    "QR-PUT-0002": mockId(idGroup.product, 202),
    "QR-PUT-0003": mockId(idGroup.product, 203),
    "QR-PUT-0004": mockId(idGroup.product, 204),
    "QR-PUT-0005": mockId(idGroup.product, 205),
    "QR-PUT-0006": mockId(idGroup.product, 206),
  },
  zeroVolumeBin: mockId(idGroup.bin, 201),
};

const { skus, bins } = seedIds;
const { cargoplaces, boxes, products } = putawayIds;

function inBuffer(qr: keyof typeof products, sku: string, cargoplace: string, box: string, status: ProductRow["status"], minutesAgo: number): ProductRow {
  return {
    product_id: products[qr],
    sku_id: sku,
    shipment_id: putawayIds.shipment,
    cargoplace_id: cargoplace,
    box_id: box,
    qr_code: qr,
    bin_id: bins["BUFFER-01"],
    order_id: null,
    status,
    created_at: ago(minutesAgo),
    updated_at: ago(minutesAgo),
  };
}

export const putawayFixtures = (): Fixture => {
  const created = ago(90);
  const tables: Fixture = {
    inbound_shipments: [{ shipment_id: putawayIds.shipment, warehouse_id: 1, ttn_code: "ТТН-2026-PUT-001", status: "GATE_CLOSED", created_at: created, updated_at: created }],
    cargoplaces: [
      { cargoplace_id: cargoplaces["CP-PUT-001"], shipment_id: putawayIds.shipment, cargoplace_code: "CP-PUT-001", status: "TABLE_CLOSED", received_at_gate_at: ago(80), created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-PUT-002"], shipment_id: putawayIds.shipment, cargoplace_code: "CP-PUT-002", status: "TABLE_IN_PROGRESS", received_at_gate_at: ago(80), created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-PUT-003"], shipment_id: putawayIds.shipment, cargoplace_code: "CP-PUT-003", status: "TABLE_CLOSED", received_at_gate_at: ago(80), created_at: created, updated_at: created },
    ],
    boxes: [
      { box_id: boxes["BOX-PUT-001"], cargoplace_id: cargoplaces["CP-PUT-001"], box_barcode: "BOX-PUT-001", status: "CLOSED", created_at: created, updated_at: created },
      { box_id: boxes["BOX-PUT-002"], cargoplace_id: cargoplaces["CP-PUT-002"], box_barcode: "BOX-PUT-002", status: "CLOSED", created_at: created, updated_at: created },
      { box_id: boxes["BOX-PUT-003"], cargoplace_id: cargoplaces["CP-PUT-003"], box_barcode: "BOX-PUT-003", status: "CLOSED", created_at: created, updated_at: created },
    ],
    bins: [{ bin_id: putawayIds.zeroVolumeBin, warehouse_id: 1, code: "C-00-00", section: "C", volume: 0, destination_id: null }],
    products: [
      inBuffer("QR-PUT-0001", skus.nike, cargoplaces["CP-PUT-001"], boxes["BOX-PUT-001"], "RECEIVED", 60),
      inBuffer("QR-PUT-0002", skus.adidas, cargoplaces["CP-PUT-001"], boxes["BOX-PUT-001"], "RECEIVED", 59),
      inBuffer("QR-PUT-0003", skus.puma, cargoplaces["CP-PUT-001"], boxes["BOX-PUT-001"], "RECEIVED", 58),
      inBuffer("QR-PUT-0004", skus.puma, cargoplaces["CP-PUT-003"], boxes["BOX-PUT-003"], "RECEIVED", 57),
      inBuffer("QR-PUT-0005", skus.adidas, cargoplaces["CP-PUT-002"], boxes["BOX-PUT-002"], "RECEIVED", 56),
      inBuffer("QR-PUT-0006", skus.tnf, cargoplaces["CP-PUT-001"], boxes["BOX-PUT-001"], "STORED", 55),
    ],
  };
  return combineFixtures(
    tables,
    signedActionFixture({
      n: 201,
      actionType: 1,
      items: [products["QR-PUT-0001"], products["QR-PUT-0002"], products["QR-PUT-0003"], products["QR-PUT-0006"]],
      details: { cargoplace_id: cargoplaces["CP-PUT-001"] },
      outcome: { status: "COMMITTED" },
      minutesAgo: 70,
    }),
    signedActionFixture({
      n: 202,
      actionType: 1,
      items: [products["QR-PUT-0004"]],
      details: { cargoplace_id: cargoplaces["CP-PUT-003"] },
      outcome: { status: "FAILED", rejectReason: 12 },
      minutesAgo: 70,
    }),
  );
};
