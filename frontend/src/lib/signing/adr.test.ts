import { hexToBigInt } from "viem";
import { describe, expect, it } from "vitest";

import { challenge, detailsHash, itemId, itemsHash, type ActionDetails } from "./adr";
import { vectors } from "./golden-vectors";

describe("ADR-0001 по golden vectors", () => {
  it("itemId совпадает с products[].itemId", () => {
    for (const product of vectors.products) {
      expect(itemId(product.productId)).toBe(hexToBigInt(product.itemId));
    }
  });

  it("itemsHash совпадает для каждого действия", () => {
    for (const action of vectors.actions) {
      expect(itemsHash(action.itemIds.map((id) => hexToBigInt(id))), action.name).toBe(action.itemsHash);
    }
  });

  it("detailsHash совпадает для складских действий", () => {
    for (const action of vectors.actions.filter((a) => a.actionType <= 4)) {
      expect(detailsHash(action.actionType, action.details as ActionDetails), action.name).toBe(action.detailsHash);
    }
  });

  it("detailsHash для ключевых действий не определён", () => {
    expect(() => detailsHash(5, { salt: "0x00", operatorId: "0", occurredAtMs: 0 })).toThrow();
  });

  it("challenge совпадает для каждого действия", () => {
    for (const action of vectors.actions) {
      expect(challenge(action.actionId, action.actionType, action.itemsHash, action.detailsHash), action.name).toBe(action.challenge);
    }
  });
});
