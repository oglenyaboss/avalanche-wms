import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";

import { apiUrl, authFetch } from "./auth-fetch";
import { getSession, saveSession } from "./session";

function token(role: string, tag: string) {
  const payload = btoa(JSON.stringify({ user_id: "u-1", role, type: "access", exp: 4102444800, tag }));
  return `h.${payload.replace(/=+$/, "")}.s`;
}

const oldAccess = token("OPERATOR", "old");
const newAccess = token("OPERATOR", "new");
const server = setupServer();
let refreshCalls = 0;

function protectedEndpoint() {
  return http.all(apiUrl("/protected"), async ({ request }) => {
    if (request.headers.get("Authorization") !== `Bearer ${newAccess}`) {
      return new HttpResponse("unauthorized\n", { status: 401 });
    }
    return HttpResponse.json({ body: request.method === "POST" ? await request.text() : null });
  });
}

function refreshEndpoint(status = 200) {
  return http.post(apiUrl("/auth/refresh"), async ({ request }) => {
    refreshCalls += 1;
    const body = (await request.json()) as { refresh_token: string };
    if (status !== 200 || body.refresh_token !== "refresh-1") return new HttpResponse("unauthorized\n", { status: 401 });
    return HttpResponse.json({ access_token: newAccess, refresh_token: "refresh-2" });
  });
}

beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
afterAll(() => server.close());
beforeEach(() => {
  refreshCalls = 0;
  localStorage.clear();
  saveSession({ access_token: oldAccess, refresh_token: "refresh-1" }, "operator");
});
afterEach(() => server.resetHandlers());

describe("authFetch", () => {
  it("отправляет access-токен в Authorization", async () => {
    server.use(http.get(apiUrl("/echo"), ({ request }) => HttpResponse.json({ auth: request.headers.get("Authorization") })));
    const response = await authFetch(apiUrl("/echo"));
    expect(await response.json()).toEqual({ auth: `Bearer ${oldAccess}` });
  });

  it("на 401 обновляет токен один раз и повторяет запрос с телом", async () => {
    server.use(protectedEndpoint(), refreshEndpoint());
    const response = await authFetch(apiUrl("/protected"), { method: "POST", body: "payload" });
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ body: "payload" });
    expect(refreshCalls).toBe(1);
    expect(getSession()).toMatchObject({ access_token: newAccess, refresh_token: "refresh-2", user: { username: "operator" } });
  });

  it("параллельные 401 делят одно обновление", async () => {
    server.use(protectedEndpoint(), refreshEndpoint());
    const responses = await Promise.all([1, 2, 3].map(() => authFetch(apiUrl("/protected"))));
    expect(responses.map((r) => r.status)).toEqual([200, 200, 200]);
    expect(refreshCalls).toBe(1);
  });

  it("запрос со старым токеном после чужого обновления повторяется без нового обновления", async () => {
    let release!: () => void;
    const released = new Promise<void>((resolve) => (release = resolve));
    server.use(
      protectedEndpoint(),
      refreshEndpoint(),
      http.get(apiUrl("/slow"), async ({ request }) => {
        if (request.headers.get("Authorization") === `Bearer ${newAccess}`) return HttpResponse.json({ ok: true });
        await released;
        return new HttpResponse("unauthorized\n", { status: 401 });
      }),
    );
    const slow = authFetch(apiUrl("/slow"));
    expect((await authFetch(apiUrl("/protected"))).status).toBe(200);
    release();
    expect((await slow).status).toBe(200);
    expect(refreshCalls).toBe(1);
  });

  it("если обновление отклонено, сессия сбрасывается и возвращается исходный 401", async () => {
    server.use(protectedEndpoint(), refreshEndpoint(401));
    const response = await authFetch(apiUrl("/protected"));
    expect(response.status).toBe(401);
    expect(refreshCalls).toBe(1);
    expect(getSession()).toBeNull();
  });

  it("повторный 401 после обновления не зацикливается", async () => {
    server.use(http.get(apiUrl("/protected"), () => new HttpResponse("unauthorized\n", { status: 401 })), refreshEndpoint());
    const response = await authFetch(apiUrl("/protected"));
    expect(response.status).toBe(401);
    expect(refreshCalls).toBe(1);
  });

  it("без сессии не пытается обновить токен", async () => {
    localStorage.clear();
    server.use(protectedEndpoint(), refreshEndpoint());
    const response = await authFetch(apiUrl("/protected"));
    expect(response.status).toBe(401);
    expect(refreshCalls).toBe(0);
  });
});
