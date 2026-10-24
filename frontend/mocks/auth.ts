import { http, HttpResponse } from "msw";

import type { Role } from "@/lib/auth/roles";

type MockUser = { id: string; username: string; password: string; role: Role };

export const users: MockUser[] = [
  { id: "01929c5e-0000-7000-8000-0000000000a1", username: "operator", password: "operator", role: "OPERATOR" },
  { id: "01929c5e-0000-7000-8000-0000000000a2", username: "admin", password: "admin", role: "ADMIN" },
  { id: "01929c5e-0000-7000-8000-0000000000a3", username: "auditor", password: "auditor", role: "AUDITOR" },
  { id: "01929c5e-0000-7000-8000-0000000000a4", username: "customer", password: "customer", role: "CUSTOMER" },
];

const accessTtlSeconds = 15 * 60;
const refreshTtlSeconds = 7 * 24 * 60 * 60;

function base64url(value: object): string {
  const bytes = new TextEncoder().encode(JSON.stringify(value));
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function makeToken(user: MockUser, type: "access" | "refresh", ttlSeconds: number): string {
  const iat = Math.floor(Date.now() / 1000);
  const claims = { user_id: user.id, role: type === "access" ? user.role : undefined, type, iat, exp: iat + ttlSeconds };
  return `${base64url({ alg: "none", typ: "JWT" })}.${base64url(claims)}.mock`;
}

function readClaims(token: string): { user_id: string; type: string; exp: number } | null {
  try {
    const payload = token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/");
    return JSON.parse(atob(payload));
  } catch {
    return null;
  }
}

function tokenPair(user: MockUser) {
  return {
    access_token: makeToken(user, "access", accessTtlSeconds),
    refresh_token: makeToken(user, "refresh", refreshTtlSeconds),
  };
}

function userFromToken(token: string | undefined, type: "access" | "refresh"): MockUser | null {
  const claims = token ? readClaims(token) : null;
  if (!claims || claims.type !== type || claims.exp * 1000 <= Date.now()) return null;
  return users.find((user) => user.id === claims.user_id) ?? null;
}

const unauthorized = () => new HttpResponse("unauthorized\n", { status: 401, headers: { "Content-Type": "text/plain" } });

export function authorize(request: Request, roles: Role[]): MockUser | Response {
  const header = request.headers.get("Authorization") ?? "";
  const user = userFromToken(header.replace(/^Bearer /, ""), "access");
  if (!user) return unauthorized();
  if (!roles.includes(user.role)) {
    return HttpResponse.json(
      { success: false, data: null, error: { code: "FORBIDDEN", message: "Недостаточно прав для этого действия" } },
      { status: 403 },
    );
  }
  return user;
}

export const authHandlers = [
  http.post("/api/auth/login", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as { username?: string; password?: string } | null;
    if (!body) {
      return new HttpResponse("invalid request body\n", { status: 400, headers: { "Content-Type": "text/plain" } });
    }
    const user = users.find((u) => u.username === body.username && u.password === body.password);
    return user ? HttpResponse.json(tokenPair(user)) : unauthorized();
  }),
  http.post("/api/auth/refresh", async ({ request }) => {
    const body = (await request.json().catch(() => null)) as { refresh_token?: string } | null;
    const user = userFromToken(body?.refresh_token, "refresh");
    return user ? HttpResponse.json(tokenPair(user)) : unauthorized();
  }),
];
