import { useEffect, useState } from "react";

import { api, unwrap, type Schema } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";

export type OperationStatus = Schema<"OperationStatus">;

export const pollIntervalMs = 2000;

const rejectReasons: Record<number, string> = {
  1: "ключ подписанта неизвестен реестру",
  2: "ключ подписанта отозван",
  3: "подпись не прошла проверку в цепи",
  4: "неверный тип действия",
  5: "подписант не администратор организации",
  6: "ключ уже зарегистрирован в реестре",
  7: "некорректный публичный ключ",
  8: "ключ из другой организации",
  9: "ключ не принадлежит оператору",
  10: "действие без товаров",
  11: "товары не по порядку",
  12: "товар не в нужном статусе",
};

export function operationVerdict(status: OperationStatus): { tone: "pending" | "done" | "error"; text: string } {
  const { parts } = status;
  const rejected = parts.find((part) => part.state === "REJECTED");
  if (rejected) {
    const reason = rejected.reject_reason === null ? "причина неизвестна" : (rejectReasons[rejected.reject_reason] ?? `код ${rejected.reject_reason}`);
    return { tone: "error", text: `Отклонено цепью: ${reason}` };
  }
  if (parts.some((part) => part.state === "FAILED")) return { tone: "error", text: "Не удалось записать в цепь" };
  if (parts.length > 0 && parts.every((part) => part.state === "APPLIED")) return { tone: "done", text: "Записано в цепь" };
  return { tone: "pending", text: "Отправлено в цепь, ждём подтверждения" };
}

export function useOperationStatus(operationId: string): { status: OperationStatus | null; error: ApiError | null } {
  const [state, setState] = useState<{ id: string; status: OperationStatus | null; error: ApiError | null }>({
    id: operationId,
    status: null,
    error: null,
  });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function poll() {
      try {
        const status = await unwrap(
          api.GET("/signed-actions/operations/{operation_id}", { params: { path: { operation_id: operationId } } }),
        );
        if (cancelled) return;
        setState({ id: operationId, status, error: null });
        if (status.parts.some((part) => part.state === "SENT")) timer = setTimeout(poll, pollIntervalMs);
      } catch (error) {
        if (cancelled) return;
        setState((prev) => ({ ...prev, id: operationId, error: error instanceof ApiError ? error : null }));
        timer = setTimeout(poll, pollIntervalMs);
      }
    }

    poll();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [operationId]);

  return state.id === operationId ? { status: state.status, error: state.error } : { status: null, error: null };
}
