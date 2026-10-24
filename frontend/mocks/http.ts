import { http, HttpResponse, type PathParams } from "msw";

import type { Role } from "@/lib/auth/roles";

import { userFromToken } from "./auth";
import { advanceChain } from "./chain";
import { db, restoreDb, saveDb, type Db, type UserRow } from "./db";

export class MockError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export const fail = (status: number, code: string, message: string) => new MockError(status, code, message);

export type Guard = { roles: Role[]; message: string };

export const guards = {
  operator: { roles: ["OPERATOR"], message: "Только оператор может выполнять это действие" },
  admin: { roles: ["ADMIN"], message: "Только администратор может выполнять это действие" },
  adminOrOperator: { roles: ["ADMIN", "OPERATOR"], message: "Требуется роль администратора или оператора" },
  auditorOrAdmin: { roles: ["AUDITOR", "ADMIN"], message: "Требуется роль аудитора или администратора" },
  authenticated: { roles: ["ADMIN", "OPERATOR", "AUDITOR", "CUSTOMER"], message: "" },
} satisfies Record<string, Guard>;

export type Ctx = { db: Db; user: UserRow; request: Request; params: PathParams; url: URL };

type Reply = { status: number; data: unknown; reply: true };

export const reply = (data: unknown, status = 200): Reply => ({ status, data, reply: true });

const isReply = (value: unknown): value is Reply => typeof value === "object" && value !== null && "reply" in value;

export const envelopeError = (status: number, code: string, message: string) =>
  HttpResponse.json({ success: false, data: null, error: { code, message } }, { status });

export const textError = (status: number, text: string) =>
  new HttpResponse(`${text}\n`, { status, headers: { "Content-Type": "text/plain; charset=utf-8" } });

let queue: Promise<unknown> = Promise.resolve();

export function serialized<T>(fn: () => Promise<T>): Promise<T> {
  const run = queue.then(fn, fn);
  queue = run.catch(() => undefined);
  return run;
}

export async function inTransaction<T>(fn: (state: Db) => Promise<T>): Promise<T> {
  const state = db();
  advanceChain(state);
  const snapshot = structuredClone(state);
  try {
    return await fn(state);
  } catch (error) {
    restoreDb(snapshot);
    throw error;
  } finally {
    saveDb();
  }
}

export function route(method: "get" | "post", path: string, guard: Guard, handler: (ctx: Ctx) => unknown) {
  return http[method](`/api${path}`, ({ request, params }) =>
    serialized(async () => {
      const user = userFromToken(request.headers.get("Authorization")?.replace(/^Bearer /, ""), "access");
      if (!user) return textError(401, "unauthorized");
      if (!guard.roles.includes(user.role)) return envelopeError(403, "FORBIDDEN", guard.message);
      try {
        const result = await inTransaction((state) => Promise.resolve(handler({ db: state, user, request, params, url: new URL(request.url) })));
        const { status, data } = isReply(result) ? result : { status: 200, data: result };
        return HttpResponse.json({ success: true, data, error: null }, { status });
      } catch (error) {
        if (error instanceof MockError) return envelopeError(error.status, error.code, error.message);
        console.error("[mocks]", error);
        return envelopeError(500, "INTERNAL_ERROR", "Внутренняя ошибка сервера");
      }
    }),
  );
}

type FieldType = "string" | "number" | "boolean" | "string[]" | "object" | "array";

const matches = (value: unknown, type: FieldType) => {
  if (value === null) return true;
  switch (type) {
    case "string[]":
      return Array.isArray(value) && value.every((item) => typeof item === "string");
    case "array":
      return Array.isArray(value);
    case "object":
      return typeof value === "object" && !Array.isArray(value);
    default:
      return typeof value === type;
  }
};

export async function readJson<Shape extends Record<string, FieldType>>(
  request: Request,
  shape: Shape,
  message: string,
  options: { allowUnknown?: boolean } = {},
): Promise<{ [K in keyof Shape]?: Shape[K] extends "string" ? string : Shape[K] extends "number" ? number : Shape[K] extends "boolean" ? boolean : Shape[K] extends "string[]" ? string[] : unknown }> {
  const body: unknown = await request.json().catch(() => undefined);
  if (typeof body !== "object" || body === null || Array.isArray(body)) throw fail(400, "INVALID_REQUEST", message);
  for (const [key, value] of Object.entries(body)) {
    const type = shape[key];
    if (!type) {
      if (options.allowUnknown) continue;
      throw fail(400, "INVALID_REQUEST", message);
    }
    if (!matches(value, type)) throw fail(400, "INVALID_REQUEST", message);
  }
  return body as never;
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const parseUuid = (value: unknown): string | null =>
  typeof value === "string" && uuidPattern.test(value) ? value.toLowerCase() : null;

export const nilUuid = "00000000-0000-0000-0000-000000000000";
