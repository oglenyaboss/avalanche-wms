import { setupServer } from "msw/node";
import { afterAll, beforeAll, beforeEach } from "vitest";

import { login } from "@/lib/auth/login";
import { clearSession } from "@/lib/auth/session";

import { chainModeKey } from "./chain";
import { resetDb } from "./db";
import { handlers } from "./handlers";
import { installVirtualAuthenticator, resetVirtualAuthenticator } from "./virtual-authenticator";

export function setupMockApi() {
  const server = setupServer(...handlers);
  beforeAll(() => {
    installVirtualAuthenticator();
    server.listen({ onUnhandledFrame: "error" });
  });
  beforeEach(() => {
    resetDb();
    clearSession();
    resetVirtualAuthenticator();
    localStorage.removeItem(chainModeKey);
  });
  afterAll(() => server.close());
}

export const loginAs = (username: string) => login(username, username);

export const waitForChain = () => new Promise((resolve) => setTimeout(resolve, 1100));
