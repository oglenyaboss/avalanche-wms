import type { OrderRow, OutboundDispatchRow, ProductRow } from "../../db";
import { ago, combineFixtures, idGroup, later, mockId, seedIds, signedActionFixture, type Fixture } from "../fixture-kit";

const { bins, dispatches, orders, products } = {
  bins: { "BIN-SHIP-5B": mockId(idGroup.bin, 401) },
  dispatches: {
    "DSP-SHP-001": mockId(idGroup.dispatch, 401),
    "DSP-SHP-002": mockId(idGroup.dispatch, 402),
    "DSP-SHP-003": mockId(idGroup.dispatch, 403),
    "DSP-SHP-004": mockId(idGroup.dispatch, 404),
    "DSP-SHP-005": mockId(idGroup.dispatch, 405),
  },
  orders: {
    "ORD-SHP-001": mockId(idGroup.order, 401),
    "ORD-SHP-002": mockId(idGroup.order, 402),
    "ORD-SHP-005": mockId(idGroup.order, 405),
    "ORD-SHP-007": mockId(idGroup.order, 407),
  },
  products: {
    "QR-SHP-0001": mockId(idGroup.product, 401),
    "QR-SHP-0002": mockId(idGroup.product, 402),
    "QR-SHP-0003": mockId(idGroup.product, 403),
    "QR-SHP-0005": mockId(idGroup.product, 405),
    "QR-SHP-0006": mockId(idGroup.product, 406),
    "QR-SHP-0007": mockId(idGroup.product, 407),
  },
};

export const shippingIds = { bins, dispatches, orders, products };

const minutesAgo = (qr: keyof typeof products) => 121 - Number(qr.slice(-4));

function order(external: keyof typeof orders, destination: keyof typeof seedIds.destinations, minutes: number): OrderRow {
  return {
    order_id: orders[external],
    external_order_no: external,
    customer_id: seedIds.users.customer,
    warehouse_id: 1,
    destination_id: seedIds.destinations[destination],
    status: "ASSEMBLED",
    created_at: ago(minutes),
    updated_at: ago(minutes),
  };
}

function readyToShip(qr: keyof typeof products, sku: string, binId: string, orderId: string): ProductRow {
  const created = ago(minutesAgo(qr));
  return {
    product_id: products[qr],
    sku_id: sku,
    shipment_id: seedIds.shipments["ТТН-2026-00142"],
    cargoplace_id: seedIds.cargoplaces["CP-001"],
    box_id: null,
    qr_code: qr,
    bin_id: binId,
    order_id: orderId,
    status: "READY_TO_SHIP",
    created_at: created,
    updated_at: created,
  };
}

function dispatchRow(input: {
  code: keyof typeof dispatches;
  destination: keyof typeof seedIds.destinations;
  status: OutboundDispatchRow["status"];
  vehicle: string;
  driver: string;
  phone: string | null;
  scheduled: number | null;
  arrived: number | null;
  departed: number | null;
}): OutboundDispatchRow {
  const created = ago(300);
  return {
    dispatch_id: dispatches[input.code],
    dispatch_code: input.code,
    warehouse_id: 1,
    destination_id: seedIds.destinations[input.destination],
    vehicle_number: input.vehicle,
    driver_name: input.driver,
    driver_phone: input.phone,
    status: input.status,
    scheduled_at: input.scheduled === null ? later(60) : ago(input.scheduled),
    arrived_at: input.arrived === null ? null : ago(input.arrived),
    departed_at: input.departed === null ? null : ago(input.departed),
    created_at: created,
    updated_at: created,
  };
}

export const shippingFixtures = (): Fixture => {
  const tables: Fixture = {
    bins: [
      { bin_id: bins["BIN-SHIP-5B"], warehouse_id: 1, code: "BIN-SHIP-5B", section: "SHIPPING_BUFFER", volume: 180, destination_id: seedIds.destinations["SHOP-5"] },
    ],
    outbound_dispatches: [
      dispatchRow({ code: "DSP-SHP-001", destination: "SHOP-5", status: "AT_GATE", vehicle: "B111AA199", driver: "Александр Смирнов", phone: "+7 (911) 111-11-11", scheduled: 60, arrived: 10, departed: null }),
      dispatchRow({ code: "DSP-SHP-002", destination: "SHOP-7", status: "AT_GATE", vehicle: "B222BB299", driver: "Дмитрий Козлов", phone: "+7 (922) 222-22-22", scheduled: 50, arrived: 20, departed: null }),
      dispatchRow({ code: "DSP-SHP-003", destination: "SHOP-5", status: "DEPARTED", vehicle: "B333CC399", driver: "Егор Волков", phone: null, scheduled: 120, arrived: 30, departed: 5 }),
      dispatchRow({ code: "DSP-SHP-004", destination: "SHOP-5", status: "CANCELLED", vehicle: "B444DD499", driver: "Никита Орлов", phone: null, scheduled: 40, arrived: null, departed: null }),
      dispatchRow({ code: "DSP-SHP-005", destination: "SHOP-7", status: "SCHEDULED", vehicle: "B555EE599", driver: "Сергей Морозов", phone: null, scheduled: null, arrived: null, departed: null }),
    ],
    orders: [
      order("ORD-SHP-001", "SHOP-5", 130),
      order("ORD-SHP-002", "SHOP-5", 130),
      order("ORD-SHP-005", "SHOP-7", 130),
      order("ORD-SHP-007", "SHOP-5", 130),
    ],
    order_lines: [
      { order_id: orders["ORD-SHP-001"], sku_id: seedIds.skus.nike, qty: 1 },
      { order_id: orders["ORD-SHP-001"], sku_id: seedIds.skus.adidas, qty: 1 },
      { order_id: orders["ORD-SHP-002"], sku_id: seedIds.skus.puma, qty: 1 },
      { order_id: orders["ORD-SHP-005"], sku_id: seedIds.skus.adidas, qty: 1 },
      { order_id: orders["ORD-SHP-005"], sku_id: seedIds.skus.nike, qty: 1 },
      { order_id: orders["ORD-SHP-007"], sku_id: seedIds.skus.nike, qty: 1 },
    ],
    products: [
      readyToShip("QR-SHP-0001", seedIds.skus.nike, seedIds.bins["BIN-SHIP-5"], orders["ORD-SHP-001"]),
      readyToShip("QR-SHP-0002", seedIds.skus.adidas, seedIds.bins["BIN-SHIP-5"], orders["ORD-SHP-001"]),
      readyToShip("QR-SHP-0003", seedIds.skus.puma, seedIds.bins["BIN-SHIP-5"], orders["ORD-SHP-002"]),
      readyToShip("QR-SHP-0005", seedIds.skus.adidas, seedIds.bins["BIN-SHIP-7"], orders["ORD-SHP-005"]),
      readyToShip("QR-SHP-0006", seedIds.skus.nike, seedIds.bins["BIN-SHIP-7"], orders["ORD-SHP-005"]),
      readyToShip("QR-SHP-0007", seedIds.skus.nike, bins["BIN-SHIP-5B"], orders["ORD-SHP-007"]),
    ],
  };
  return combineFixtures(
    tables,
    signedActionFixture({
      n: 401,
      actionType: 3,
      items: [products["QR-SHP-0001"], products["QR-SHP-0002"], products["QR-SHP-0003"], products["QR-SHP-0006"], products["QR-SHP-0007"]],
      outcome: { status: "COMMITTED" },
      minutesAgo: 30,
    }),
  );
};
