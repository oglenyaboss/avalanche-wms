import type { Db, OnchainEventRow, OutboxEventRow, WarehouseActionRow } from "../db";

import { seededKeys } from "./keys";

export type Tables = Omit<Db, "version">;
export type Fixture = { [K in keyof Tables]?: Tables[K] };

export const idGroup = {
  user: 0x8000,
  bin: 0x8001,
  sku: 0x8002,
  shipment: 0x8003,
  cargoplace: 0x8004,
  box: 0x8005,
  product: 0x8006,
  order: 0x8007,
  destination: 0x8008,
  dispatch: 0x8009,
  action: 0x800a,
  operation: 0x800b,
  event: 0x800c,
} as const;

export function mockId(group: number, n: number): string {
  return `01929c5e-0000-7000-${group.toString(16)}-${n.toString().padStart(12, "0")}`;
}

export const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
export const later = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();

export const seedIds = {
  org: "01929c5e-0000-7000-8000-00000000a001",
  users: {
    operator: "01929c5e-0000-7000-8000-0000000000a1",
    admin: "01929c5e-0000-7000-8000-0000000000a2",
    auditor: "01929c5e-0000-7000-8000-0000000000a3",
    customer: "01929c5e-0000-7000-8000-0000000000a4",
    operator2: "01929c5e-0000-7000-8000-0000000000a5",
  },
  destinations: {
    "SHOP-5": mockId(idGroup.destination, 1),
    "SHOP-7": mockId(idGroup.destination, 2),
  },
  bins: {
    "A-01-01": mockId(idGroup.bin, 1),
    "A-01-02": mockId(idGroup.bin, 2),
    "A-02-01": mockId(idGroup.bin, 3),
    "B-01-01": mockId(idGroup.bin, 4),
    "B-01-02": mockId(idGroup.bin, 5),
    "BUFFER-01": mockId(idGroup.bin, 6),
    "BIN-SHIP-5": mockId(idGroup.bin, 7),
    "BIN-SHIP-7": mockId(idGroup.bin, 8),
  },
  skus: {
    nike: mockId(idGroup.sku, 1),
    adidas: mockId(idGroup.sku, 2),
    puma: mockId(idGroup.sku, 3),
    tnf: mockId(idGroup.sku, 4),
    e2e: mockId(idGroup.sku, 5),
  },
  shipments: {
    "ТТН-2026-00142": mockId(idGroup.shipment, 1),
    "ТТН-2026-TABLE-001": mockId(idGroup.shipment, 2),
    "ТТН-2026-TABLE-002": mockId(idGroup.shipment, 3),
    "ТТН-2026-GATE-001": mockId(idGroup.shipment, 4),
    "ТТН-2026-GATE-002": mockId(idGroup.shipment, 5),
  },
  cargoplaces: {
    "CP-001": mockId(idGroup.cargoplace, 1),
    "CP-002": mockId(idGroup.cargoplace, 2),
    "CP-TABLE-001": mockId(idGroup.cargoplace, 3),
    "CP-TABLE-002": mockId(idGroup.cargoplace, 4),
    "CP-GATE-001": mockId(idGroup.cargoplace, 5),
    "CP-GATE-002": mockId(idGroup.cargoplace, 6),
    "CP-GATE-003": mockId(idGroup.cargoplace, 7),
    "CP-GATE-004": mockId(idGroup.cargoplace, 8),
  },
  boxes: {
    "BOX-001": mockId(idGroup.box, 1),
    "BOX-002": mockId(idGroup.box, 2),
    "BOX-003": mockId(idGroup.box, 3),
    "BOX-TABLE-201": mockId(idGroup.box, 4),
  },
  orders: {
    "ORD-2026-0042": mockId(idGroup.order, 1),
    "ORD-2026-0043": mockId(idGroup.order, 2),
    "ORD-2026-0501": mockId(idGroup.order, 3),
    "ORD-2026-0502": mockId(idGroup.order, 4),
  },
  dispatches: {
    "DSP-2026-0421-001": mockId(idGroup.dispatch, 1),
  },
} as const;

const credentialOf = (username: string) => seededKeys.find((key) => key.username === username)!.credentialId;

export const seededCredentialIds = { operator: credentialOf("operator"), admin: credentialOf("admin") };

type ChainOutcome = { status: "COMMITTED" } | { status: "SENT" } | { status: "FAILED"; rejectReason: number | null };

export function signedActionFixture(input: {
  n: number;
  actionType: WarehouseActionRow["action_type"];
  items: string[];
  createdBy?: string;
  details?: Record<string, unknown>;
  outcome: ChainOutcome | null;
  minutesAgo: number;
}): Fixture {
  const actionId = mockId(idGroup.action, input.n);
  const at = ago(input.minutesAgo);
  const zero = `0x${"00".repeat(32)}`;
  const action: WarehouseActionRow = {
    action_id: actionId,
    operation_id: mockId(idGroup.operation, input.n),
    part_index: 0,
    action_type: input.actionType,
    signer_credential_id: seededCredentialIds.operator,
    created_by: input.createdBy ?? seedIds.users.operator,
    item_ids: input.items,
    salt: zero,
    details_hash: zero,
    challenge: zero,
    details: input.details ?? {},
    created_at: at,
    submitted_at: at,
  };
  const event: OnchainEventRow | null =
    input.outcome === null
      ? null
      : {
          event_id: actionId,
          aggregate_type: "signed_action",
          tx_hash: `0x${input.n.toString(16).padStart(64, "0")}`,
          status: input.outcome.status,
          error_message: input.outcome.status === "FAILED" ? "rejected by contract" : null,
          reject_reason: input.outcome.status === "FAILED" ? input.outcome.rejectReason : null,
          created_at: at,
          updated_at: at,
          settles_at: null,
          settles_to: null,
        };
  const outbox: OutboxEventRow = {
    event_id: actionId,
    aggregate_id: actionId,
    aggregate_type: "signed_action",
    event_type: "wms.action.v1",
    payload_hash: zero.slice(2),
    payload: { version: 1, actionId, actionType: input.actionType, itemIds: input.items, detailsHash: zero },
    created_at: at,
  };
  return event ? { warehouse_actions: [action], outbox_events: [outbox], onchain_events: [event] } : { warehouse_actions: [action], outbox_events: [outbox] };
}

export function mergeFixtures(target: Tables, ...fixtures: Fixture[]) {
  for (const fixture of fixtures) {
    for (const key of Object.keys(fixture) as (keyof Tables)[]) {
      (target[key] as unknown[]).push(...(fixture[key] as unknown[]));
    }
  }
}

export function combineFixtures(...fixtures: Fixture[]): Fixture {
  const out: Record<string, unknown[]> = {};
  for (const fixture of fixtures) {
    for (const [key, rows] of Object.entries(fixture)) out[key] = [...(out[key] ?? []), ...(rows as unknown[])];
  }
  return out as Fixture;
}
