// The Desktop's client state (PLAN.md §4.3). In M3.0 it holds only the boot
// phase and the machine Info; M3.1 grows it into the event-stream-fed store that
// keeps several browser tabs in sync.
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "zustand";

import type { InfoResponse } from "./gen/aos/v1/services_pb";
import { auth, system } from "./api/client";

export type Phase = "loading" | "needs-signin" | "ready" | "error";

interface DesktopState {
  phase: Phase;
  error: string;
  info?: InfoResponse;
  boot: () => Promise<void>;
}

// signIn exchanges a one-time code from the URL hash (#code=…) for the session
// cookie, then removes it from the address bar (PLAN.md §7.6).
async function signIn(): Promise<void> {
  const hash = new URLSearchParams(window.location.hash.slice(1));
  const code = hash.get("code");
  if (!code) return;
  await auth.exchangeLoginCode({ code });
  history.replaceState(null, "", window.location.pathname + window.location.search);
}

export const useDesktop = create<DesktopState>((set) => ({
  phase: "loading",
  error: "",
  boot: async () => {
    try {
      await signIn();
      const info = await system.info({});
      set({ phase: "ready", info });
    } catch (err) {
      // No valid session yet: send the user to `aos desktop-url` (PLAN.md §7.6).
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        set({ phase: "needs-signin" });
        return;
      }
      set({ phase: "error", error: ConnectError.from(err).message });
    }
  },
}));
