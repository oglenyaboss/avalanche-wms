import { bytesToHex, hexToBytes, type Hex } from "viem";

import type { Schema } from "@/lib/api/client";
import { challenge, itemId, itemsHash } from "@/lib/signing/adr";
import { fromBase64url, toBase64url } from "@/lib/signing/base64url";
import { assertionToTuple, verifyWebAuthn } from "@/lib/signing/webauthn-verify";

import { enqueueChainEvent } from "./chain";
import { newId, now, type Db, type PasskeyRow, type WarehouseActionRow } from "./db";
import { fail, parseUuid, readJson } from "./http";

export const maxItemsPerAction = 2000;

export type Operation = {
  operation_id: string;
  action_type: WarehouseActionRow["action_type"];
  created_by: string;
  signer_credential_id: string;
  details: Record<string, unknown>;
  parts: WarehouseActionRow[];
  submitted: boolean;
};

export type Assertion = Schema<"WebAuthnAssertion">;

export const relyingParty = () => ({ rpId: location.hostname, origins: [location.origin] });

export function latestPasskey(state: Db, userId: string): PasskeyRow | undefined {
  return state.passkey_credentials.filter((key) => key.user_id === userId).sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
}

export function activeSigner(state: Db, userId: string, message = "Ключ оператора не активен в реестре"): PasskeyRow {
  const signer = latestPasskey(state, userId);
  if (!signer || signer.status !== "ACTIVE") throw fail(409, "SIGNER_KEY_NOT_ACTIVE", message);
  return signer;
}

export const randomHex32 = (): Hex => bytesToHex(crypto.getRandomValues(new Uint8Array(32)));

export const sortByItemId = (ids: string[]) =>
  ids
    .map((id) => ({ id, key: itemId(id) }))
    .sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0))
    .map((entry) => entry.id);

export function createOperation(
  state: Db,
  input: {
    actionType: WarehouseActionRow["action_type"];
    createdBy: string;
    signer: PasskeyRow;
    details: Record<string, unknown>;
    items: string[];
    detailsHash: (salt: Hex, partItems: string[]) => Hex;
  },
): Operation {
  const operationId = newId();
  const sorted = sortByItemId(input.items);
  const createdAt = now();
  const parts: WarehouseActionRow[] = [];
  for (let start = 0; start < sorted.length; start += maxItemsPerAction) {
    const items = sorted.slice(start, start + maxItemsPerAction);
    const actionId = newId();
    const salt = randomHex32();
    const detailsHash = input.detailsHash(salt, items);
    parts.push({
      action_id: actionId,
      operation_id: operationId,
      part_index: parts.length,
      action_type: input.actionType,
      signer_credential_id: input.signer.credential_id,
      created_by: input.createdBy,
      item_ids: items,
      salt,
      details_hash: detailsHash,
      challenge: challenge(actionId, input.actionType, itemsHash(items.map(itemId)), detailsHash),
      details: input.details,
      created_at: createdAt,
      submitted_at: null,
    });
  }
  state.warehouse_actions.push(...parts);
  return {
    operation_id: operationId,
    action_type: input.actionType,
    created_by: input.createdBy,
    signer_credential_id: input.signer.credential_id,
    details: input.details,
    parts,
    submitted: false,
  };
}

export function draftView(state: Db, operation: Operation): Schema<"SignedActionDraft"> {
  const signer = state.passkey_credentials.find((key) => key.credential_id === operation.signer_credential_id);
  return {
    operation_id: operation.operation_id,
    action_type: operation.action_type,
    signer_key_id: signer?.key_id ?? "",
    rp_id: relyingParty().rpId,
    allow_credentials: [{ type: "public-key", id: operation.signer_credential_id }],
    user_verification: "required",
    timeout: 60000,
    parts: operation.parts.map((part) => ({
      action_id: part.action_id,
      item_ids: part.item_ids,
      details_hash: part.details_hash,
      challenge: toBase64url(hexToBytes(part.challenge as Hex)),
    })),
  };
}

export function loadOperation(state: Db, operationId: string): Operation | null {
  const parts = state.warehouse_actions.filter((action) => action.operation_id === operationId).sort((a, b) => a.part_index - b.part_index);
  if (parts.length === 0) return null;
  return {
    operation_id: operationId,
    action_type: parts[0].action_type,
    created_by: parts[0].created_by,
    signer_credential_id: parts[0].signer_credential_id,
    details: parts[0].details,
    parts,
    submitted: parts.some((part) => part.submitted_at !== null),
  };
}

export function requireDraft(state: Db, operationId: string, userId: string, actionType: Operation["action_type"]): Operation {
  const operation = loadOperation(state, operationId);
  if (!operation || operation.created_by !== userId || operation.action_type !== actionType) {
    throw fail(404, "OPERATION_NOT_FOUND", "Операция не найдена");
  }
  if (operation.submitted) throw fail(409, "OPERATION_ALREADY_SUBMITTED", "Операция уже отправлена");
  return operation;
}

export function requireOperationSigner(state: Db, operation: Operation, userId: string, message?: string): PasskeyRow {
  const signer = activeSigner(state, userId, message);
  if (signer.credential_id !== operation.signer_credential_id) {
    throw fail(409, "SIGNER_KEY_NOT_ACTIVE", "Ключ подписанта не активен в реестре");
  }
  return signer;
}

const base64urlPattern = /^[A-Za-z0-9_-]*$/;
const isBase64url = (value: unknown): value is string => typeof value === "string" && base64urlPattern.test(value) && value.length % 4 !== 1;

const assertionKeys = ["action_id", "credential_id", "authenticator_data", "client_data_json", "signature"];

export async function readSubmit(request: Request, message: string): Promise<{ operationId: string; assertions: Map<string, Assertion> }> {
  const body = await readJson(request, { operation_id: "string", assertions: "array" }, message);
  const operationId = parseUuid(body.operation_id);
  if (!operationId) throw fail(400, "INVALID_REQUEST", message);
  const assertions = new Map<string, Assertion>();
  for (const raw of (body.assertions as unknown[] | undefined) ?? []) {
    if (typeof raw !== "object" || raw === null || Object.keys(raw).some((key) => !assertionKeys.includes(key))) {
      throw fail(400, "INVALID_REQUEST", message);
    }
    const entry = raw as Record<string, unknown>;
    const actionId = parseUuid(entry.action_id);
    const fields = [entry.credential_id ?? "", entry.authenticator_data ?? "", entry.client_data_json ?? "", entry.signature ?? ""];
    if (!actionId || assertions.has(actionId) || !fields.every(isBase64url)) throw fail(400, "INVALID_REQUEST", message);
    const [credential_id, authenticator_data, client_data_json, signature] = fields as string[];
    assertions.set(actionId, { credential_id, authenticator_data, client_data_json, signature });
  }
  return { operationId, assertions };
}

const invalidAssertion = () => fail(400, "INVALID_ASSERTION", "Подтверждение не прошло проверку");

export async function verifyAssertion(assertion: Assertion, challengeHex: Hex, signer: PasskeyRow): Promise<void> {
  if (assertion.credential_id !== signer.credential_id) throw invalidAssertion();
  let clientData: { type?: unknown; challenge?: unknown; origin?: unknown };
  try {
    clientData = JSON.parse(new TextDecoder().decode(fromBase64url(assertion.client_data_json)));
  } catch {
    throw invalidAssertion();
  }
  const { rpId, origins } = relyingParty();
  if (clientData.type !== "webauthn.get") throw invalidAssertion();
  if (clientData.challenge !== toBase64url(hexToBytes(challengeHex))) throw invalidAssertion();
  if (typeof clientData.origin !== "string" || !origins.includes(clientData.origin)) throw invalidAssertion();

  const authenticatorData = fromBase64url(assertion.authenticator_data);
  if (authenticatorData.length < 37) throw invalidAssertion();
  const rpIdHash = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(rpId)));
  if (!rpIdHash.every((byte, i) => byte === authenticatorData[i])) throw invalidAssertion();
  const flags = authenticatorData[32];
  if ((flags & 0x10) !== 0 && (flags & 0x08) === 0) throw invalidAssertion();

  let tuple;
  try {
    tuple = assertionToTuple(assertion);
  } catch {
    throw invalidAssertion();
  }
  if (!(await verifyWebAuthn(tuple, challengeHex, signer.qx as Hex, signer.qy as Hex))) throw invalidAssertion();
}

export async function verifyOperation(operation: Operation, signer: PasskeyRow, assertions: Map<string, Assertion>) {
  if (assertions.size !== operation.parts.length) throw invalidAssertion();
  for (const part of operation.parts) {
    const assertion = assertions.get(part.action_id);
    if (!assertion) throw invalidAssertion();
    await verifyAssertion(assertion, part.challenge as Hex, signer);
  }
}

export function lockSigner(state: Db, credentialId: string) {
  const key = state.passkey_credentials.find((passkey) => passkey.credential_id === credentialId);
  const revoked = state.key_actions.some((action) => {
    if (action.target_credential_id !== credentialId || action.action_type !== 6 || action.submitted_at === null) return false;
    return state.onchain_events.find((event) => event.event_id === action.action_id)?.status !== "FAILED";
  });
  if (!key || key.status !== "ACTIVE" || revoked) throw fail(409, "SIGNER_KEY_NOT_ACTIVE", "Ключ подписанта не активен в реестре");
}

export function writeActions(state: Db, operation: Operation, assertions: Map<string, Assertion>) {
  const submittedAt = now();
  for (const part of operation.parts) {
    state.outbox_events.push({
      event_id: part.action_id,
      aggregate_id: part.action_id,
      aggregate_type: "signed_action",
      event_type: "wms.action.v1",
      payload_hash: part.challenge.slice(2),
      payload: {
        version: 1,
        actionId: part.action_id,
        actionType: part.action_type,
        itemIds: part.item_ids,
        detailsHash: part.details_hash,
        webauthn: assertions.get(part.action_id),
      },
      created_at: submittedAt,
    });
    enqueueChainEvent(state, part.action_id, "signed_action");
    part.submitted_at = submittedAt;
  }
  operation.submitted = true;
}

export function partByItem(operation: Operation): Map<string, string> {
  const map = new Map<string, string>();
  for (const part of operation.parts) for (const id of part.item_ids) map.set(id, part.action_id);
  return map;
}

export const occurredAtNow = () => {
  const ms = Date.now();
  return { ms, iso: new Date(ms).toISOString() };
};
