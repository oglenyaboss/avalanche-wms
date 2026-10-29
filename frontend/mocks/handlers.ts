import { authHandlers } from "./auth";
import { assemblyHandlers } from "./domains/assembly";
import { healthHandlers } from "./domains/health";
import { historyHandlers } from "./domains/history";
import { passkeysHandlers } from "./domains/passkeys";
import { putawayHandlers } from "./domains/putaway";
import { receivingHandlers } from "./domains/receiving";
import { shippingHandlers } from "./domains/shipping";
import { signedActionsHandlers } from "./domains/signed-actions";

export const handlers = [
  ...healthHandlers,
  ...authHandlers,
  ...receivingHandlers,
  ...putawayHandlers,
  ...assemblyHandlers,
  ...shippingHandlers,
  ...passkeysHandlers,
  ...signedActionsHandlers,
  ...historyHandlers,
];
