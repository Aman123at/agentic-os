// Host-aware keyboard shortcuts (PLAN.md §4.3). The container is always Linux;
// the Host is the user's own machine, so we read it from the browser. Only
// Spotlight differs per Host (Ctrl+Space on Windows); Close and Switch use Alt
// everywhere. Remapping in System Settings arrives in M4.
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

export function installShortcuts(handlers: Shortcuts): () => void {
  const spotlightWithCtrl = hostOS() === "win";
  const onKey = (e: KeyboardEvent) => {
    const spotlight = spotlightWithCtrl ? e.ctrlKey : e.altKey;
    if (spotlight && e.code === "Space") {
      e.preventDefault();
      handlers.spotlight();
    } else if (e.altKey && (e.key === "w" || e.key === "W")) {
      e.preventDefault();
      handlers.closeWindow();
    } else if (e.altKey && e.key === "`") {
      e.preventDefault();
      handlers.switchWindow();
    }
  };
  window.addEventListener("keydown", onKey);
  return () => window.removeEventListener("keydown", onKey);
}
