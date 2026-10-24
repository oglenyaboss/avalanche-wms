import type { Db, OnchainEventRow, WarehouseActionRow } from "./db";

export type ChainMode = "commit" | "fail" | "hold" | `reject:${number}`;

export const chainModeKey = "wms-mock-chain";
export const chainDelayMs = 1000;

export function chainMode(): ChainMode {
  return (localStorage.getItem(chainModeKey) as ChainMode | null) ?? "commit";
}

export function setChainMode(mode: ChainMode) {
  localStorage.setItem(chainModeKey, mode);
}

function plannedOutcome(): OnchainEventRow["settles_to"] {
  const mode = chainMode();
  if (mode === "hold") return null;
  if (mode === "fail") return { status: "FAILED", reject_reason: null };
  if (mode.startsWith("reject:")) return { status: "FAILED", reject_reason: Number(mode.slice("reject:".length)) };
  return { status: "COMMITTED", reject_reason: null };
}

export function enqueueChainEvent(state: Db, eventId: string, aggregateType: string) {
  const at = new Date();
  state.onchain_events.push({
    event_id: eventId,
    aggregate_type: aggregateType,
    tx_hash: null,
    status: "PENDING",
    error_message: null,
    reject_reason: null,
    created_at: at.toISOString(),
    updated_at: at.toISOString(),
    settles_at: new Date(at.getTime() + chainDelayMs).toISOString(),
    settles_to: plannedOutcome(),
  });
}

export function advanceChain(state: Db) {
  const now = new Date().toISOString();
  for (const event of state.onchain_events) {
    if (!event.settles_to || !event.settles_at || event.settles_at > now) continue;
    event.status = event.settles_to.status;
    event.reject_reason = event.settles_to.reject_reason;
    event.error_message = event.status === "FAILED" ? "rejected by contract" : null;
    event.tx_hash = `0x${event.event_id.replaceAll("-", "").padEnd(64, "0")}`;
    event.updated_at = now;
    event.settles_at = null;
    event.settles_to = null;
    applyRegistry(state, event);
  }
}

function applyRegistry(state: Db, event: OnchainEventRow) {
  const action = state.key_actions.find((keyAction) => keyAction.action_id === event.event_id);
  if (!action || event.status !== "COMMITTED") return;
  const key = state.passkey_credentials.find((passkey) => passkey.credential_id === action.target_credential_id);
  if (!key) return;
  key.status = action.action_type === 5 ? "ACTIVE" : "REVOKED";
  key.updated_at = event.updated_at;
}

export const chainAggregate = { receiving: 1, putaway: 2, picking: 3, shipping: 4 } as const satisfies Record<string, WarehouseActionRow["action_type"]>;

export type ChainAggregate = keyof typeof chainAggregate;

const eventOf = (state: Db, eventId: string) => state.onchain_events.find((event) => event.event_id === eventId);

export function chainEventRejected(state: Db, productIds: string[], aggregate: ChainAggregate): boolean {
  const ids = new Set(productIds);
  return state.warehouse_actions.some(
    (action) =>
      action.action_type === chainAggregate[aggregate] &&
      action.item_ids.some((id) => ids.has(id)) &&
      eventOf(state, action.action_id)?.status === "FAILED",
  );
}

export function unconfirmedProducts(state: Db, productIds: string[], aggregate: ChainAggregate): string[] {
  const confirmed = (productId: string) =>
    state.warehouse_actions.some(
      (action) =>
        action.action_type === chainAggregate[aggregate] &&
        action.submitted_at !== null &&
        action.item_ids.includes(productId) &&
        eventOf(state, action.action_id)?.status === "COMMITTED",
    );
  return productIds.filter((id) => !confirmed(id)).sort();
}
