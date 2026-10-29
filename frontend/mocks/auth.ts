import { http, HttpResponse } from "msw";

import type { Role } from "@/lib/auth/roles";

import { db, newId, now, type UserRow } from "./db";
import { inTransaction, serialized, textError } from "./http";

const accessTtlSeconds = 15 * 60;
const refreshTtlSeconds = 7 * 24 * 60 * 60;
const roles: Role[] = ["ADMIN", "OPERATOR", "CUSTOMER", "AUDITOR"];

function base64url(value: object): string {
  const bytes = new TextEncoder().encode(JSON.stringify(value));
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function makeToken(user: UserRow, type: "access" | "refresh", ttlSeconds: number): string {
  const iat = Math.floor(Date.now() / 1000);
  const claims = { user_id: user.user_id, role: type === "access" ? user.role : undefined, type, iat, exp: iat + ttlSeconds };
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

function tokenPair(user: UserRow) {
  return {
    access_token: makeToken(user, "access", accessTtlSeconds),
    refresh_token: makeToken(user, "refresh", refreshTtlSeconds),
  };
}

export function userFromToken(token: string | undefined, type: "access" | "refresh"): UserRow | null {
  const claims = token ? readClaims(token) : null;
  if (!claims || claims.type !== type || claims.exp * 1000 <= Date.now()) return null;
  return db().users.find((user) => user.user_id === claims.user_id && user.is_active) ?? null;
}

async function readBody(request: Request, keys: string[]): Promise<Record<string, string> | null> {
  const body: unknown = await request.json().catch(() => null);
  if (typeof body !== "object" || body === null || Array.isArray(body)) return null;
  const entries = Object.entries(body);
  if (entries.some(([key, value]) => !keys.includes(key) || (value !== null && typeof value !== "string"))) return null;
  return body as Record<string, string>;
}

const invalidBody = () => textError(400, "invalid request body");

export const authHandlers = [
  http.post("/api/auth/login", ({ request }) =>
    serialized(async () => {
      const body = await readBody(request, ["username", "password"]);
      if (!body) return invalidBody();
      const user = db().users.find((u) => u.username === body.username && u.password === body.password && u.is_active);
      return user ? HttpResponse.json(tokenPair(user)) : textError(401, "unauthorized");
    }),
  ),
  http.post("/api/auth/refresh", ({ request }) =>
    serialized(async () => {
      const body = await readBody(request, ["refresh_token"]);
      if (!body) return invalidBody();
      const user = userFromToken(body.refresh_token, "refresh");
      return user ? HttpResponse.json(tokenPair(user)) : textError(401, "unauthorized");
    }),
  ),
  http.post("/api/auth/register", ({ request }) =>
    serialized(async () => {
      const actor = userFromToken(request.headers.get("Authorization")?.replace(/^Bearer /, ""), "access");
      if (!actor) return textError(401, "unauthorized");
      const body = await readBody(request, ["username", "password", "role"]);
      if (!body) return invalidBody();
      if (actor.role !== "ADMIN") return textError(403, "forbidden");
      const { username = "", password = "", role = "" } = body;
      const passwordLength = new TextEncoder().encode(password).length;
      if (!username || passwordLength < 6 || passwordLength > 72 || !roles.includes(role as Role)) return invalidBody();
      return inTransaction(async (state) => {
        if (state.users.some((user) => user.username === username)) return textError(409, "user already exists");
        const user: UserRow = { user_id: newId(), username, password, role: role as Role, is_active: true, org_id: null, created_at: now() };
        state.users.push(user);
        return HttpResponse.json({ id: user.user_id, username: user.username }, { status: 201 });
      });
    }),
  ),
];
