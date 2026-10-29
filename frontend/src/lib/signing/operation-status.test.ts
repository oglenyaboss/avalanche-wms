import { describe, expect, it } from "vitest";

import { operationVerdict, type OperationStatus } from "./operation-status";

const status = (...states: [OperationStatus["parts"][number]["state"], number | null][]): OperationStatus => ({
  operation_id: "op",
  parts: states.map(([state, reject_reason], i) => ({ action_id: `a${i}`, state, reject_reason })),
});

describe("operationVerdict", () => {
  it.each([
    [status(["APPLIED", null], ["APPLIED", null]), "done", "Записано в цепь"],
    [status(["APPLIED", null], ["SENT", null]), "pending", "Отправлено в цепь, ждём подтверждения"],
    [status(["APPLIED", null], ["REJECTED", 12]), "error", "Отклонено цепью: товар не в нужном статусе"],
    [status(["REJECTED", 99]), "error", "Отклонено цепью: код 99"],
    [status(["REJECTED", null]), "error", "Отклонено цепью: причина неизвестна"],
    [status(["FAILED", null], ["APPLIED", null]), "error", "Не удалось записать в цепь"],
  ])("%# → %s", (input, tone, text) => {
    expect(operationVerdict(input)).toEqual({ tone, text });
  });
});
