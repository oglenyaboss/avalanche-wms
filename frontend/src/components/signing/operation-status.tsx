"use client";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { operationVerdict, useOperationStatus } from "@/lib/signing/operation-status";

export function OperationStatus({ operationId }: { operationId: string }) {
  const { status, error } = useOperationStatus(operationId);
  const verdict = status ? operationVerdict(status) : { tone: "pending" as const, text: "Отправлено в цепь, ждём подтверждения" };

  return (
    <Alert role="status" variant={verdict.tone === "error" ? "destructive" : "default"} data-tone={verdict.tone}>
      <AlertTitle>{verdict.text}</AlertTitle>
      {error && verdict.tone === "pending" && <AlertDescription>Статус пока не получен: {error.message}</AlertDescription>}
    </Alert>
  );
}
