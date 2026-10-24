import createClient from "openapi-fetch";

import { authFetch } from "@/lib/auth/auth-fetch";

import { networkError, toApiError } from "./errors";
import type { components, paths } from "./schema";

export type Schema<Name extends keyof components["schemas"]> = components["schemas"][Name];

export const api = createClient<paths>({ baseUrl: "/api", fetch: authFetch });

export const publicApi = createClient<paths>({ baseUrl: "/api", fetch: (request) => fetch(request) });

type Payload<T> = T extends { success: true; data?: infer Data } ? Exclude<Data, undefined> : T;

export async function unwrap<T>(request: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<Payload<T>> {
  let result: Awaited<typeof request>;
  try {
    result = await request;
  } catch {
    throw networkError();
  }
  if (!result.response.ok) throw toApiError(result.response.status, result.error);
  const data = result.data as T;
  return (isEnvelope(data) ? data.data : data) as Payload<T>;
}

function isEnvelope(data: unknown): data is { success: true; data: unknown } {
  return typeof data === "object" && data !== null && "success" in data && "data" in data;
}
