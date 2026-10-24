import { useSyncExternalStore } from "react";

import { decodeAccessToken } from "./jwt";
import type { Role } from "./roles";

export type Tokens = { access_token: string; refresh_token: string };

export type Session = Tokens & {
  user: { id: string; role: Role; username: string };
};

const storageKey = "wms-auth";
const listeners = new Set<() => void>();
let cached: { raw: string | null; session: Session | null } = { raw: null, session: null };

function parse(raw: string | null): Session | null {
  if (!raw) return null;
  try {
    const stored = JSON.parse(raw) as Tokens & { username: string };
    const claims = decodeAccessToken(stored.access_token);
    if (!claims || !stored.refresh_token) return null;
    return {
      access_token: stored.access_token,
      refresh_token: stored.refresh_token,
      user: { id: claims.user_id, role: claims.role, username: stored.username },
    };
  } catch {
    return null;
  }
}

export function getSession(): Session | null {
  if (typeof window === "undefined") return null;
  const raw = window.localStorage.getItem(storageKey);
  if (raw !== cached.raw) cached = { raw, session: parse(raw) };
  return cached.session;
}

export function saveSession(tokens: Tokens, username: string): Session {
  const raw = JSON.stringify({ ...tokens, username });
  const session = parse(raw);
  if (!session) throw new Error("Сервер вернул некорректный токен");
  window.localStorage.setItem(storageKey, raw);
  notify();
  return session;
}

export function clearSession() {
  window.localStorage.removeItem(storageKey);
  notify();
}

function notify() {
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  const onStorage = (event: StorageEvent) => {
    if (event.key === storageKey) listener();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", onStorage);
  };
}

export function useSession(): Session | null {
  return useSyncExternalStore(subscribe, getSession, () => null);
}
