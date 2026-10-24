import type { ExpectedCargoplaceSkuRow, ProductRow } from "../../db";
import { ago, idGroup, mockId, seedIds, type Fixture } from "../fixture-kit";

export const receivingIds = {
  shipments: {
    "ТТН-2026-RCV-001": mockId(idGroup.shipment, 101),
    "ТТН-2026-RCV-002": mockId(idGroup.shipment, 102),
  },
  cargoplaces: {
    "CP-RCV-001": mockId(idGroup.cargoplace, 111),
    "CP-RCV-002": mockId(idGroup.cargoplace, 112),
    "CP-RCV-003": mockId(idGroup.cargoplace, 113),
    "CP-RCV-101": mockId(idGroup.cargoplace, 121),
    "CP-RCV-102": mockId(idGroup.cargoplace, 122),
    "CP-RCV-103": mockId(idGroup.cargoplace, 123),
    "CP-RCV-104": mockId(idGroup.cargoplace, 124),
  },
  boxes: {
    "BOX-RCV-101": mockId(idGroup.box, 131),
    "BOX-RCV-102": mockId(idGroup.box, 132),
    "BOX-RCV-103": mockId(idGroup.box, 133),
    "BOX-RCV-104": mockId(idGroup.box, 134),
  },
  products: {
    "QR-RCV-0001": mockId(idGroup.product, 141),
    "QR-RCV-0002": mockId(idGroup.product, 142),
    "QR-RCV-0003": mockId(idGroup.product, 143),
  },
};

export const receivingFixtures = (): Fixture => {
  const created = ago(90);
  const atGate = ago(60);
  const { shipments, cargoplaces, boxes, products } = receivingIds;
  const { skus } = seedIds;
  const cp101 = cargoplaces["CP-RCV-101"];
  const cp102 = cargoplaces["CP-RCV-102"];
  const cp103 = cargoplaces["CP-RCV-103"];
  const cp104 = cargoplaces["CP-RCV-104"];
  const shipmentClosed = shipments["ТТН-2026-RCV-002"];

  const product = (qr: keyof typeof products, sku: string, cargoplace: string, box: string, minutesAgo: number): ProductRow => ({
    product_id: products[qr],
    sku_id: sku,
    shipment_id: shipmentClosed,
    cargoplace_id: cargoplace,
    box_id: box,
    qr_code: qr,
    bin_id: null,
    order_id: null,
    status: "RECEIVED",
    created_at: ago(minutesAgo),
    updated_at: ago(minutesAgo),
  });

  const expected = (cargoplace: string, sku: string, expected_qty: number): ExpectedCargoplaceSkuRow => ({ cargoplace_id: cargoplace, sku_id: sku, expected_qty });

  return {
    inbound_shipments: [
      { shipment_id: shipments["ТТН-2026-RCV-001"], warehouse_id: 1, ttn_code: "ТТН-2026-RCV-001", status: "GATE_IN_PROGRESS", created_at: created, updated_at: created },
      { shipment_id: shipmentClosed, warehouse_id: 1, ttn_code: "ТТН-2026-RCV-002", status: "GATE_CLOSED", created_at: created, updated_at: created },
    ],
    cargoplaces: [
      { cargoplace_id: cargoplaces["CP-RCV-001"], shipment_id: shipments["ТТН-2026-RCV-001"], cargoplace_code: "CP-RCV-001", status: "RECEIVED_AT_GATE", received_at_gate_at: atGate, created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-RCV-002"], shipment_id: shipments["ТТН-2026-RCV-001"], cargoplace_code: "CP-RCV-002", status: "EXPECTED", received_at_gate_at: null, created_at: created, updated_at: created },
      { cargoplace_id: cargoplaces["CP-RCV-003"], shipment_id: shipments["ТТН-2026-RCV-001"], cargoplace_code: "CP-RCV-003", status: "EXPECTED", received_at_gate_at: null, created_at: created, updated_at: created },
      { cargoplace_id: cp101, shipment_id: shipmentClosed, cargoplace_code: "CP-RCV-101", status: "TABLE_IN_PROGRESS", received_at_gate_at: atGate, created_at: created, updated_at: created },
      { cargoplace_id: cp102, shipment_id: shipmentClosed, cargoplace_code: "CP-RCV-102", status: "TABLE_IN_PROGRESS", received_at_gate_at: atGate, created_at: created, updated_at: created },
      { cargoplace_id: cp103, shipment_id: shipmentClosed, cargoplace_code: "CP-RCV-103", status: "TABLE_CLOSED", received_at_gate_at: atGate, created_at: created, updated_at: created },
      { cargoplace_id: cp104, shipment_id: shipmentClosed, cargoplace_code: "CP-RCV-104", status: "RECEIVED_AT_GATE", received_at_gate_at: atGate, created_at: created, updated_at: created },
    ],
    boxes: [
      { box_id: boxes["BOX-RCV-101"], cargoplace_id: cp101, box_barcode: "BOX-RCV-101", status: "OPEN", created_at: created, updated_at: created },
      { box_id: boxes["BOX-RCV-102"], cargoplace_id: cp101, box_barcode: "BOX-RCV-102", status: "CLOSED", created_at: created, updated_at: created },
      { box_id: boxes["BOX-RCV-103"], cargoplace_id: cp102, box_barcode: "BOX-RCV-103", status: "CLOSED", created_at: created, updated_at: created },
      { box_id: boxes["BOX-RCV-104"], cargoplace_id: cp103, box_barcode: "BOX-RCV-104", status: "OPEN", created_at: created, updated_at: created },
    ],
    products: [
      product("QR-RCV-0001", skus.nike, cp101, boxes["BOX-RCV-101"], 50),
      product("QR-RCV-0002", skus.nike, cp101, boxes["BOX-RCV-102"], 49),
      product("QR-RCV-0003", skus.adidas, cp101, boxes["BOX-RCV-102"], 48),
    ],
    expected_cargoplace_skus: [
      expected(cp101, skus.nike, 3),
      expected(cp101, skus.adidas, 2),
      expected(cp104, skus.puma, 1),
      expected(cp104, skus.nike, 2),
    ],
  };
};
