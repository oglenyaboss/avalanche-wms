import { concatHex, encodeAbiParameters, hexToBigInt, keccak256, toBytes, toHex, type Hex } from "viem";

export const actionType = {
  Receive: 1,
  PutAway: 2,
  Pick: 3,
  Ship: 4,
  KeyRegister: 5,
  KeyRevoke: 6,
} as const;

export type ActionDetails = {
  salt: Hex;
  cargoplaceId?: string;
  storageBinId?: string;
  shippingBufferBinId?: string;
  dispatchId?: string;
  orderIds?: string[];
  operatorId: string;
  occurredAtMs: number;
};

export function uuidToBytes16(uuid: string): Hex {
  return `0x${uuid.replaceAll("-", "")}`;
}

export function itemId(productId: string): bigint {
  return hexToBigInt(keccak256(toBytes(productId.toLowerCase())));
}

export function itemsHash(itemIds: readonly bigint[]): Hex {
  return keccak256(concatHex(itemIds.map((id) => toHex(id, { size: 32 }))));
}

const salted = (fields: { type: string }[], values: unknown[]) => keccak256(encodeAbiParameters(fields, values));

export function detailsHash(type: number, d: ActionDetails): Hex {
  const operator = uuidToBytes16(d.operatorId);
  const occurredAt = BigInt(d.occurredAtMs);
  switch (type) {
    case actionType.Receive:
      return salted(
        [{ type: "bytes32" }, { type: "bytes16" }, { type: "bytes16" }, { type: "uint64" }],
        [d.salt, uuidToBytes16(d.cargoplaceId!), operator, occurredAt],
      );
    case actionType.PutAway:
      return salted(
        [{ type: "bytes32" }, { type: "bytes16" }, { type: "bytes16" }, { type: "uint64" }],
        [d.salt, uuidToBytes16(d.storageBinId!), operator, occurredAt],
      );
    case actionType.Pick:
      return salted(
        [{ type: "bytes32" }, { type: "bytes16" }, { type: "bytes16[]" }, { type: "bytes16" }, { type: "uint64" }],
        [d.salt, uuidToBytes16(d.shippingBufferBinId!), sortedOrderIds(d), operator, occurredAt],
      );
    case actionType.Ship:
      return salted(
        [
          { type: "bytes32" },
          { type: "bytes16" },
          { type: "bytes16" },
          { type: "bytes16[]" },
          { type: "bytes16" },
          { type: "uint64" },
        ],
        [d.salt, uuidToBytes16(d.dispatchId!), uuidToBytes16(d.shippingBufferBinId!), sortedOrderIds(d), operator, occurredAt],
      );
    default:
      throw new Error(`detailsHash не определён для action_type ${type}`);
  }
}

function sortedOrderIds(d: ActionDetails): Hex[] {
  const ids = [...new Set((d.orderIds ?? []).map((id) => id.toLowerCase()))];
  ids.sort();
  return ids.map(uuidToBytes16);
}

const typehash = keccak256(toBytes("avalanche-wms/SignedAction/v1"));

export function challenge(actionId: string, type: number, itemsHashHex: Hex, detailsHashHex: Hex): Hex {
  return keccak256(
    encodeAbiParameters(
      [{ type: "bytes32" }, { type: "bytes16" }, { type: "uint8" }, { type: "bytes32" }, { type: "bytes32" }],
      [typehash, uuidToBytes16(actionId), type, itemsHashHex, detailsHashHex],
    ),
  );
}
