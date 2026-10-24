import { render, screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, beforeAll, expect, it } from "vitest";

import { OperationStatus } from "./operation-status";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
afterAll(() => server.close());

it("опрашивает статус, пока есть SENT, и показывает итог", async () => {
  const states = ["SENT", "APPLIED"];
  let calls = 0;
  server.use(
    http.get("/api/signed-actions/operations/:id", ({ params }) => {
      const state = states[Math.min(calls++, states.length - 1)];
      return HttpResponse.json({
        success: true,
        data: { operation_id: params.id, parts: [{ action_id: "a1", state, reject_reason: null }] },
        error: null,
      });
    }),
  );

  render(<OperationStatus operationId="01929c5e-6f00-7000-8000-000000002000" />);

  expect((await screen.findByRole("status")).textContent).toContain("Отправлено в цепь, ждём подтверждения");
  expect(await screen.findByText("Записано в цепь", {}, { timeout: 3000 })).toBeTruthy();
  await new Promise((resolve) => setTimeout(resolve, 2200));
  expect(calls).toBe(2);
}, 10000);
