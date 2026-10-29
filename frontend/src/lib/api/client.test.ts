import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, expectTypeOf, it } from "vitest";

import { login } from "@/lib/auth/login";

import { api, publicApi, unwrap, type Schema } from "./client";
import { ApiError } from "./errors";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
afterAll(() => server.close());
afterEach(() => {
  server.resetHandlers();
  localStorage.clear();
});

const scanBuffer = () => api.POST("/putaway/scan-buffer", { body: { buffer_bin_id: "66666666-6666-6666-6666-666666666666" } });

async function caught(promise: Promise<unknown>): Promise<ApiError> {
  const error = await promise.catch((e: unknown) => e);
  expect(error).toBeInstanceOf(ApiError);
  return error as ApiError;
}

describe("unwrap", () => {
  it("снимает конверт и отдаёт data с типом из openapi", async () => {
    const payload = { buffer_bin_id: "66666666-6666-6666-6666-666666666666", buffer_code: "BUF-01", products: [], total_products: 0 };
    server.use(http.post("/api/putaway/scan-buffer", () => HttpResponse.json({ success: true, data: payload, error: null })));
    const data = await unwrap(scanBuffer());
    expectTypeOf(data).toEqualTypeOf<Schema<"PutawayScanBufferResponse">>();
    expect(data).toEqual(payload);
  });

  it("data: null в конверте отдаётся как null", async () => {
    server.use(http.get("/api/passkeys/me", () => HttpResponse.json({ success: true, data: null, error: null })));
    const data = await unwrap(api.GET("/passkeys/me"));
    expectTypeOf(data).toEqualTypeOf<Schema<"PasskeyView"> | null>();
    expect(data).toBeNull();
  });

  it("ответ без конверта отдаётся как есть", async () => {
    server.use(http.post("/api/auth/refresh", () => HttpResponse.json({ access_token: "a", refresh_token: "r" })));
    const data = await unwrap(publicApi.POST("/auth/refresh", { body: { refresh_token: "r" } }));
    expectTypeOf(data).toEqualTypeOf<Schema<"TokenPair">>();
    expect(data).toEqual({ access_token: "a", refresh_token: "r" });
  });

  it("ошибка в конверте — ApiError с кодом и сообщением бэкенда", async () => {
    server.use(
      http.post("/api/putaway/scan-buffer", () =>
        HttpResponse.json(
          { success: false, data: null, error: { code: "BUFFER_BIN_NOT_FOUND", message: "Буферная ячейка не найдена" } },
          { status: 404 },
        ),
      ),
    );
    const error = await caught(unwrap(scanBuffer()));
    expect({ status: error.status, code: error.code, message: error.message }).toEqual({
      status: 404,
      code: "BUFFER_BIN_NOT_FOUND",
      message: "Буферная ячейка не найдена",
    });
  });

  it("ошибка простым текстом — тот же ApiError с кодом", async () => {
    server.use(http.post("/api/putaway/scan-buffer", () => new HttpResponse("forbidden\n", { status: 403, headers: { "Content-Type": "text/plain" } })));
    const error = await caught(unwrap(scanBuffer()));
    expect({ status: error.status, code: error.code }).toEqual({ status: 403, code: "FORBIDDEN" });
  });

  it("401 простым текстом без сессии — UNAUTHORIZED", async () => {
    server.use(http.post("/api/putaway/scan-buffer", () => new HttpResponse("unauthorized\n", { status: 401, headers: { "Content-Type": "text/plain" } })));
    const error = await caught(unwrap(scanBuffer()));
    expect({ status: error.status, code: error.code }).toEqual({ status: 401, code: "UNAUTHORIZED" });
  });

  it("неизвестное тело ошибки — HTTP_<статус>", async () => {
    server.use(http.post("/api/putaway/scan-buffer", () => new HttpResponse("<html>Bad Gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } })));
    const error = await caught(unwrap(scanBuffer()));
    expect({ status: error.status, code: error.code }).toEqual({ status: 502, code: "HTTP_502" });
  });

  it("обрыв сети — NETWORK_ERROR", async () => {
    server.use(http.post("/api/putaway/scan-buffer", () => HttpResponse.error()));
    const error = await caught(unwrap(scanBuffer()));
    expect({ status: error.status, code: error.code }).toEqual({ status: 0, code: "NETWORK_ERROR" });
  });
});

describe("login", () => {
  it("неверный пароль — ApiError «Неверный логин или пароль»", async () => {
    server.use(http.post("/api/auth/login", () => new HttpResponse("unauthorized\n", { status: 401, headers: { "Content-Type": "text/plain" } })));
    const error = await caught(login("operator", "wrong"));
    expect({ code: error.code, message: error.message }).toEqual({ code: "INVALID_CREDENTIALS", message: "Неверный логин или пароль" });
  });
});
