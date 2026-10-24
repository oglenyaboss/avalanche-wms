import type { Role } from "@/lib/auth/roles";

import { dbVersion, seed } from "./data/seed";

export type UserRow = {
  user_id: string;
  username: string;
  password: string;
  role: Role;
  is_active: boolean;
  org_id: string | null;
  created_at: string;
};

export type OrganizationRow = { org_id: string; name: string; created_at: string };

export type WarehouseRow = { warehouse_id: number; name: string; address: string; contact: string };

export type DestinationRow = { destination_id: string; code: string; name: string; address: string | null; warehouse_id: number };

export type BinRow = {
  bin_id: string;
  warehouse_id: number;
  code: string;
  section: string | null;
  volume: number | null;
  destination_id: string | null;
};

export type SkuRow = { sku_id: string; name: string; description: string | null; volume: number | null };

export type SkuBarcodeRow = { sku_id: string; barcode: string };

export type InboundShipmentRow = {
  shipment_id: string;
  warehouse_id: number;
  ttn_code: string;
  status: string;
  created_at: string;
  updated_at: string;
};

export type CargoplaceRow = {
  cargoplace_id: string;
  shipment_id: string;
  cargoplace_code: string;
  status: string;
  received_at_gate_at: string | null;
  created_at: string;
  updated_at: string;
};

export type BoxRow = { box_id: string; cargoplace_id: string; box_barcode: string; status: string; created_at: string; updated_at: string };

export type ExpectedCargoplaceSkuRow = { cargoplace_id: string; sku_id: string; expected_qty: number };

export type OrderStatus = "NEW" | "ALLOCATED" | "ASSEMBLED" | "PARTIALLY_SHIPPED" | "SHIPPED";

export type OrderRow = {
  order_id: string;
  external_order_no: string;
  customer_id: string;
  warehouse_id: number;
  destination_id: string;
  status: OrderStatus;
  created_at: string;
  updated_at: string;
};

export type OrderLineRow = { order_id: string; sku_id: string; qty: number };

export type ProductStatus = "RECEIVED" | "STORED" | "ALLOCATED" | "ASSEMBLED" | "READY_TO_SHIP" | "SHIPPED";

export type ProductRow = {
  product_id: string;
  sku_id: string;
  shipment_id: string;
  cargoplace_id: string;
  box_id: string | null;
  qr_code: string;
  bin_id: string | null;
  order_id: string | null;
  status: ProductStatus;
  created_at: string;
  updated_at: string;
};

export type DispatchStatus = "SCHEDULED" | "AT_GATE" | "DEPARTED" | "CANCELLED";

export type OutboundDispatchRow = {
  dispatch_id: string;
  dispatch_code: string;
  warehouse_id: number;
  destination_id: string;
  vehicle_number: string;
  driver_name: string;
  driver_phone: string | null;
  status: DispatchStatus;
  scheduled_at: string;
  arrived_at: string | null;
  departed_at: string | null;
  created_at: string;
  updated_at: string;
};

export type ReceivingGateRow = {
  event_id: string;
  ttn_code: string | null;
  cargoplace_code: string | null;
  shipment_id: string | null;
  cargoplace_id: string | null;
  operator_id: string;
  action: string;
  occurred_at: string;
  created_at: string;
};

export type ReceivingTableRow = {
  event_id: string;
  cargoplace_id: string;
  box_id: string | null;
  operator_id: string;
  action: string;
  box_barcode: string | null;
  sku_id: string | null;
  qr_code: string | null;
  product_id: string | null;
  buffer_bin_id: string | null;
  operation_id: string | null;
  occurred_at: string;
  created_at: string;
};

export type PutawayRow = {
  event_id: string;
  product_id: string;
  from_bin_id: string | null;
  bin_id: string;
  operator_id: string;
  action_id: string | null;
  occurred_at: string;
  created_at: string;
};

export type TaskStatus = "PENDING" | "IN_PROGRESS" | "DONE" | "CANCELLED";

export type AssemblyTaskRow = {
  event_id: string;
  order_id: string;
  product_id: string;
  sku_id: string;
  from_bin_id: string;
  section: string | null;
  destination_id: string;
  status: TaskStatus;
  operator_id: string | null;
  action_id: string | null;
  occurred_at: string | null;
  unloaded_at: string | null;
  created_at: string;
  updated_at: string;
};

export type ShippingRow = {
  event_id: string;
  product_id: string;
  operator_id: string;
  dispatch_id: string;
  action_id: string | null;
  shipped_at: string;
  occurred_at: string;
  created_at: string;
};

export type PasskeyStatus = "PENDING" | "ACTIVE" | "REJECTED" | "REVOKED";

export type PasskeyRow = {
  credential_id: string;
  user_id: string;
  org_id: string;
  qx: string;
  qy: string;
  key_id: string;
  role: "ADMIN" | "OPERATOR";
  status: PasskeyStatus;
  created_at: string;
  updated_at: string;
};

export type KeyActionRow = {
  action_id: string;
  org_id: string;
  action_type: 5 | 6;
  target_credential_id: string;
  signer_credential_id: string;
  challenge: string;
  created_by: string;
  created_at: string;
  submitted_at: string | null;
};

export type RegistrationSessionRow = {
  user_id: string;
  challenge: string;
  expires_at: string;
};

export type WarehouseActionRow = {
  action_id: string;
  operation_id: string;
  part_index: number;
  action_type: 1 | 2 | 3 | 4;
  signer_credential_id: string;
  created_by: string;
  item_ids: string[];
  salt: string;
  details_hash: string;
  challenge: string;
  details: Record<string, unknown>;
  created_at: string;
  submitted_at: string | null;
};

export type OutboxEventRow = {
  event_id: string;
  aggregate_id: string;
  aggregate_type: string;
  event_type: string;
  payload_hash: string;
  payload: unknown;
  created_at: string;
};

export type OnchainStatus = "PENDING" | "SENT" | "COMMITTED" | "FAILED";

export type OnchainEventRow = {
  event_id: string;
  aggregate_type: string;
  tx_hash: string | null;
  status: OnchainStatus;
  error_message: string | null;
  reject_reason: number | null;
  created_at: string;
  updated_at: string;
  settles_at: string | null;
  settles_to: { status: "COMMITTED" | "FAILED"; reject_reason: number | null } | null;
};

export type Db = {
  version: number;
  users: UserRow[];
  organizations: OrganizationRow[];
  warehouses: WarehouseRow[];
  destinations: DestinationRow[];
  bins: BinRow[];
  skus: SkuRow[];
  sku_barcodes: SkuBarcodeRow[];
  inbound_shipments: InboundShipmentRow[];
  cargoplaces: CargoplaceRow[];
  boxes: BoxRow[];
  expected_cargoplace_skus: ExpectedCargoplaceSkuRow[];
  orders: OrderRow[];
  order_lines: OrderLineRow[];
  products: ProductRow[];
  outbound_dispatches: OutboundDispatchRow[];
  receiving_gate: ReceivingGateRow[];
  receiving_table: ReceivingTableRow[];
  putaways: PutawayRow[];
  assembly_tasks: AssemblyTaskRow[];
  shippings: ShippingRow[];
  passkey_credentials: PasskeyRow[];
  key_actions: KeyActionRow[];
  registration_sessions: RegistrationSessionRow[];
  warehouse_actions: WarehouseActionRow[];
  outbox_events: OutboxEventRow[];
  onchain_events: OnchainEventRow[];
};

const storageKey = "wms-mock-db";

let current: Db | null = null;

export function db(): Db {
  if (!current) {
    const saved = localStorage.getItem(storageKey);
    const parsed = saved ? (JSON.parse(saved) as Db) : null;
    current = parsed?.version === dbVersion ? parsed : seed();
  }
  return current;
}

export function saveDb() {
  if (current) localStorage.setItem(storageKey, JSON.stringify(current));
}

export function resetDb() {
  localStorage.removeItem(storageKey);
  current = null;
}

export function restoreDb(snapshot: Db) {
  current = snapshot;
}

export const now = () => new Date().toISOString();

export function newId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const ms = Date.now();
  for (let i = 0; i < 6; i++) bytes[i] = Math.floor(ms / 2 ** (8 * (5 - i))) & 0xff;
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
