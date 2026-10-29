import { http, HttpResponse } from "msw";

export const healthHandlers = [
  http.get("/api/health", () =>
    HttpResponse.json({
      status: "ok",
      checks: { postgres: "ok", kafka: "ok", ledger_adapter: "ok" },
      time: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
    }),
  ),
];
