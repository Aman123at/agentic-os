// Keyboard shortcuts that follow the user's own computer (PLAN.md §4.3). The
// Machine is always Linux; the shortcuts should match whatever the user's real
// keyboard prints, so we read the OS from the browser and pick the per-OS
// defaults (see shortcuts.ts). System Settings remaps them, and the map
// installShortcuts matches against comes from there.
import { matches, type ShortcutMap } from "./shortcuts";

type BrowserOS = "mac" | "win" | "linux";

interface NavigatorUAData {
  platform?: string;
}

export function browserOS(): BrowserOS {
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
