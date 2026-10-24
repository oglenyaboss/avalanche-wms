import type { Schema } from "@/lib/api/client";

import type { Db } from "../db";
import { fail, guards, parseUuid, route } from "../http";

type PartStatus = Schema<"OperationStatus">["parts"][number];

function confirmationOf(state: Db, actionId: string): Pick<PartStatus, "state" | "reject_reason"> {
  const event = state.onchain_events.find((row) => row.event_id === actionId);
  if (event?.status === "COMMITTED") return { state: "APPLIED", reject_reason: null };
  if (event?.status === "FAILED") return event.reject_reason === null ? { state: "FAILED", reject_reason: null } : { state: "REJECTED", reject_reason: event.reject_reason };
  return { state: "SENT", reject_reason: null };
}

export const signedActionsHandlers = [
  route("get", "/signed-actions/operations/:operationId", guards.adminOrOperator, ({ db, user, params }) => {
    const operationId = parseUuid(params.operationId);
    if (!operationId) throw fail(400, "INVALID_REQUEST", "Некорректный запрос");
    const parts = db.warehouse_actions
      .filter((action) => action.operation_id === operationId && action.created_by === user.user_id)
      .sort((a, b) => a.part_index - b.part_index);
    if (parts.length === 0) throw fail(404, "OPERATION_NOT_FOUND", "Операция не найдена");
    if (parts.some((part) => part.submitted_at === null)) throw fail(409, "OPERATION_NOT_SUBMITTED", "Операция ещё не отправлена");
    return {
      operation_id: operationId,
      parts: parts.map((part) => ({ action_id: part.action_id, ...confirmationOf(db, part.action_id) })),
    } satisfies Schema<"OperationStatus">;
  }),
];
