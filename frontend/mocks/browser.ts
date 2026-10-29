import { setupWorker } from "msw/browser";

import { handlers } from "./handlers";
import { installVirtualAuthenticator } from "./virtual-authenticator";

installVirtualAuthenticator();

export const worker = setupWorker(...handlers);
