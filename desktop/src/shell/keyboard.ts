// Host-aware keyboard shortcuts (PLAN.md §4.3). The container is always Linux;
// the Host is the user's own machine, so we read it from the browser, which
// decides the per-Host defaults (see shortcuts.ts). System Settings remaps them,
// and the map installShortcuts matches against comes from there.
import { matches, type ShortcutMap } from "./shortcuts";

type HostOS = "mac" | "win" | "linux";

interface NavigatorUAData {
  platform?: string;
}

export function hostOS(): HostOS {
  const nav = navigator as Navigator & { userAgentData?: NavigatorUAData };
  const platform = (nav.userAgentData?.platform || navigator.platform || "").toLowerCase();
  if (platform.includes("mac")) return "mac";
  if (platform.includes("win")) return "win";
  return "linux";
}

export interface Shortcuts {
  spotlight: () => void;
  closeWindow: () => void;
  switchWindow: () => void;
}

export function installShortcuts(handlers: Shortcuts, map: ShortcutMap): () => void {
  const onKey = (e: KeyboardEvent) => {
    if (matches(e, map.spotlight)) {
      e.preventDefault();
      handlers.spotlight();
    } else if (matches(e, map.closeWindow)) {
      e.preventDefault();
      handlers.closeWindow();
    } else if (matches(e, map.switchWindow)) {
      e.preventDefault();
      handlers.switchWindow();
    }
  };
  window.addEventListener("keydown", onKey);
  return () => window.removeEventListener("keydown", onKey);
}
