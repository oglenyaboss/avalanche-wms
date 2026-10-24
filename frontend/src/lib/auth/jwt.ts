import type { Role } from "./roles";

export type AccessClaims = {
  user_id: string;
  role: Role;
  exp: number;
};

export function decodeAccessToken(token: string): AccessClaims | null {
  try {
    const payload = token.split(".")[1];
    const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
    const claims = JSON.parse(new TextDecoder().decode(Uint8Array.from(json, (c) => c.charCodeAt(0))));
    if (claims.type !== "access" || typeof claims.user_id !== "string" || typeof claims.role !== "string") {
      return null;
    }
    return { user_id: claims.user_id, role: claims.role, exp: claims.exp };
  } catch {
    return null;
  }
}
