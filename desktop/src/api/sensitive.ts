// Sensitive System Files (PLAN.md M8.5): in Root Mode aosd refuses to change
// one unless the request confirms it, and says which file and why. The
// Desktop asks with SensitiveConfirm and sends the request again, confirmed.
import { ConnectError } from "@connectrpc/connect";

import { SensitivePathSchema, type SensitivePath } from "../gen/aos/v1/services_pb";

// sensitiveOf returns the file a refused change would have touched, or null
// when err is anything else.
export function sensitiveOf(err: unknown): SensitivePath | null {
  if (!(err instanceof ConnectError)) return null;
  return err.findDetails(SensitivePathSchema)[0] ?? null;
}
