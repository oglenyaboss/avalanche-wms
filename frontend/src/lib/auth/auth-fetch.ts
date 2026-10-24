import { clearSession, getSession, saveSession, type Tokens } from "./session";

let refreshing: Promise<string | null> | null = null;

export async function authFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const sentToken = getSession()?.access_token;
  const response = await fetch(withToken(request.clone(), sentToken));
  if (response.status !== 401 || !sentToken) return response;

  const current = getSession()?.access_token;
  const token = current && current !== sentToken ? current : await refreshOnce();
  if (!token) return response;
  return fetch(withToken(request, token));
}

function withToken(request: Request, token: string | undefined): Request {
  if (token) request.headers.set("Authorization", `Bearer ${token}`);
  return request;
}

function refreshOnce(): Promise<string | null> {
  refreshing ??= refresh().finally(() => {
    refreshing = null;
  });
  return refreshing;
}

async function refresh(): Promise<string | null> {
  const session = getSession();
  if (!session) return null;
  const response = await fetch(apiUrl("/auth/refresh"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refresh_token: session.refresh_token }),
  });
  if (response.status === 400 || response.status === 401) {
    clearSession();
    return null;
  }
  if (!response.ok) return null;
  const tokens = (await response.json()) as Tokens;
  return saveSession(tokens, session.user.username).access_token;
}

export function apiUrl(path: string): string {
  return new URL(`/api${path}`, window.location.origin).toString();
}
