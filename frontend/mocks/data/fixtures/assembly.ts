import type { AssemblyTaskRow, OrderLineRow, OrderRow, OrderStatus, ProductRow, TaskStatus } from "../../db";
import { ago, combineFixtures, idGroup, mockId, seedIds, signedActionFixture, type Fixture } from "../fixture-kit";

export const assemblyIds = {
  destinations: { "SHOP-9": mockId(idGroup.destination, 301) },
  bins: { "BIN-SHIP-9": mockId(idGroup.bin, 301), "BIN-SHIP-0": mockId(idGroup.bin, 302) },
  products: {
    "QR-ASM-0001": mockId(idGroup.product, 311),
    "QR-ASM-0002": mockId(idGroup.product, 312),
    "QR-ASM-0010": mockId(idGroup.product, 320),
    "QR-ASM-0011": mockId(idGroup.product, 321),
    "QR-ASM-0020": mockId(idGroup.product, 330),
    "QR-ASM-0030": mockId(idGroup.product, 340),
  },
  orders: {
    "ORD-ASM-001": mockId(idGroup.order, 350),
    "ORD-ASM-002": mockId(idGroup.order, 351),
    "ORD-ASM-003": mockId(idGroup.order, 352),
    "ORD-ASM-010": mockId(idGroup.order, 360),
    "ORD-ASM-020": mockId(idGroup.order, 370),
    "ORD-ASM-030": mockId(idGroup.order, 380),
  },
  tasks: {
    "QR-ASM-0010": mockId(idGroup.event, 361),
    "QR-ASM-0011": mockId(idGroup.event, 362),
    "QR-ASM-0020": mockId(idGroup.event, 371),
    "QR-ASM-0030": mockId(idGroup.event, 381),
  },
};

const { products, orders, tasks } = assemblyIds;
const seedBins = seedIds.bins;

type ProductQr = keyof typeof products & keyof typeof tasks;
type SeedBinCode = keyof typeof seedBins;

function assemblyProduct(qr: keyof typeof products, sku: keyof typeof seedIds.skus, bin: SeedBinCode | null, order: keyof typeof orders | null, status: ProductRow["status"], minutesAgo: number): ProductRow {
  return {
    product_id: products[qr],
    sku_id: seedIds.skus[sku],
    shipment_id: seedIds.shipments["ТТН-2026-00142"],
    cargoplace_id: seedIds.cargoplaces["CP-001"],
    box_id: seedIds.boxes["BOX-001"],
    qr_code: qr,
    bin_id: bin ? seedBins[bin] : null,
    order_id: order ? orders[order] : null,
    status,
    created_at: ago(minutesAgo),
    updated_at: ago(minutesAgo),
  };
}

function assemblyOrder(key: keyof typeof orders, destination: string, status: OrderStatus, minutesAgo: number): OrderRow {
  return { order_id: orders[key], external_order_no: key, customer_id: seedIds.users.customer, warehouse_id: 1, destination_id: destination, status, created_at: ago(minutesAgo), updated_at: ago(minutesAgo) };
}

function assemblyOrderLine(key: keyof typeof orders, sku: keyof typeof seedIds.skus, qty: number): OrderLineRow {
  return { order_id: orders[key], sku_id: seedIds.skus[sku], qty };
}

function assemblyTask(qr: ProductQr, sku: keyof typeof seedIds.skus, bin: SeedBinCode, section: string, order: keyof typeof orders, destination: string, status: TaskStatus, operatorId: string | null, minutesAgo: number): AssemblyTaskRow {
  const at = ago(minutesAgo);
  return {
    event_id: tasks[qr],
    order_id: orders[order],
    product_id: products[qr],
    sku_id: seedIds.skus[sku],
    from_bin_id: seedBins[bin],
    section,
    destination_id: destination,
    status,
    operator_id: operatorId,
    action_id: null,
    occurred_at: status === "DONE" ? at : null,
    unloaded_at: null,
    created_at: at,
    updated_at: at,
  };
}

export const assemblyFixtures = (): Fixture => {
  const shop9 = assemblyIds.destinations["SHOP-9"];
  return combineFixtures(
    {
      destinations: [{ destination_id: shop9, code: "SHOP-9", name: "Магазин №9 Тверская", address: "г. Москва, ул. Тверская, 9", warehouse_id: 1 }],
      bins: [
        { bin_id: assemblyIds.bins["BIN-SHIP-9"], warehouse_id: 1, code: "BIN-SHIP-9", section: "SHIPPING_BUFFER", volume: 180, destination_id: shop9 },
        { bin_id: assemblyIds.bins["BIN-SHIP-0"], warehouse_id: 1, code: "BIN-SHIP-0", section: "SHIPPING_BUFFER", volume: 180, destination_id: null },
      ],
      products: [
        assemblyProduct("QR-ASM-0001", "puma", "A-01-02", null, "STORED", 150),
        assemblyProduct("QR-ASM-0002", "puma", "B-01-01", null, "STORED", 149),
        assemblyProduct("QR-ASM-0010", "nike", "A-02-01", "ORD-ASM-010", "ALLOCATED", 80),
        assemblyProduct("QR-ASM-0011", "adidas", "B-01-02", "ORD-ASM-010", "ALLOCATED", 79),
        assemblyProduct("QR-ASM-0020", "nike", "A-01-01", "ORD-ASM-020", "ASSEMBLED", 70),
        assemblyProduct("QR-ASM-0030", "puma", "A-01-01", "ORD-ASM-030", "ALLOCATED", 65),
      ],
      orders: [
        assemblyOrder("ORD-ASM-001", shop9, "NEW", 100),
        assemblyOrder("ORD-ASM-002", shop9, "NEW", 99),
        assemblyOrder("ORD-ASM-003", shop9, "NEW", 98),
        assemblyOrder("ORD-ASM-010", shop9, "ALLOCATED", 90),
        assemblyOrder("ORD-ASM-020", seedIds.destinations["SHOP-7"], "ALLOCATED", 85),
        assemblyOrder("ORD-ASM-030", shop9, "ALLOCATED", 84),
      ],
      order_lines: [
        assemblyOrderLine("ORD-ASM-001", "puma", 2),
        assemblyOrderLine("ORD-ASM-002", "puma", 5),
        assemblyOrderLine("ORD-ASM-003", "adidas", 1),
        assemblyOrderLine("ORD-ASM-010", "nike", 1),
        assemblyOrderLine("ORD-ASM-010", "adidas", 1),
        assemblyOrderLine("ORD-ASM-020", "nike", 1),
        assemblyOrderLine("ORD-ASM-030", "puma", 1),
      ],
      assembly_tasks: [
        assemblyTask("QR-ASM-0010", "nike", "A-02-01", "A", "ORD-ASM-010", shop9, "PENDING", null, 80),
        assemblyTask("QR-ASM-0011", "adidas", "B-01-02", "B", "ORD-ASM-010", shop9, "PENDING", null, 79),
        assemblyTask("QR-ASM-0020", "nike", "A-01-01", "A", "ORD-ASM-020", seedIds.destinations["SHOP-7"], "DONE", seedIds.users.operator, 40),
        assemblyTask("QR-ASM-0030", "puma", "A-01-01", "A", "ORD-ASM-030", shop9, "PENDING", null, 65),
      ],
    },
    signedActionFixture({
      n: 301,
      actionType: 2,
      items: [products["QR-ASM-0030"]],
      outcome: { status: "FAILED", rejectReason: 7 },
      minutesAgo: 60,
    }),
  );
};
