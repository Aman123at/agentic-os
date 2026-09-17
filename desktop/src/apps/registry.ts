// The Desktop's apps. Each is a lazily loaded chunk (PLAN.md §4.3 rule 3), so
// the shell stays small and an app's code is fetched only when it first opens.
// Each app also exposes preload(), which fetches that chunk without opening the
// app: the shell warms them while the Machine is idle, so the first open is not
// a download (PLAN.md §16, the first visible Agent step).
import { lazy, type ComponentType, type LazyExoticComponent } from "react";

import type { InfoResponse } from "../gen/aos/v1/services_pb";

export type AppId = "about" | "finder" | "terminal" | "agent" | "browser" | "preview" | "textedit" | "trash" | "activity" | "software" | "settings";

export interface AppDef {
  id: AppId;
  name: string;
  icon: string; // an emoji for now; the drawn SVG icon set arrives in M3.5
  size: { w: number; h: number };
  singleton?: boolean;
  inDock?: boolean;
  Component: LazyExoticComponent<ComponentType>;
  // Fetches the app's chunk now. Calling it twice is free: the module registry
  // returns the same promise.
  preload: () => Promise<unknown>;
}

// app builds the two halves of a lazily loaded app from one import factory, so
// the chunk React suspends on and the chunk preload() fetches are the same one.
function app(load: () => Promise<{ default: ComponentType }>) {
  return { Component: lazy(load), preload: load as () => Promise<unknown> };
}

export const APPS: Record<AppId, AppDef> = {
  about: {
    id: "about",
    name: "About This Machine",
    icon: "🖥️",
    size: { w: 380, h: 300 },
    singleton: true,
    ...app(() => import("./About")),
  },
  finder: {
    id: "finder",
    name: "Finder",
    icon: "🗂️",
    size: { w: 760, h: 480 },
    inDock: true,
    ...app(() => import("./Finder")),
  },
  terminal: {
    id: "terminal",
    name: "Terminal",
    icon: "⌨️",
    size: { w: 660, h: 420 },
    inDock: true,
    ...app(() => import("./Terminal")),
  },
  // The Agent app: Tasks, the Audit Log and Usage. Spotlight, notifications and
  // Approvals open it at a Task, so there is only ever one.
  agent: {
    id: "agent",
    name: "Agent",
    icon: "🤖",
    size: { w: 900, h: 580 },
    singleton: true,
    inDock: true,
    ...app(() => import("./agent/Agent")),
  },
  // The Browser: a page Chromium renders inside the Machine, streamed into the
  // window (PLAN.md M5.2). Only in Machines built with INCLUDE_BROWSER=true;
  // see appShown.
  browser: {
    id: "browser",
    name: "Browser",
    icon: "🌐",
    size: { w: 1024, h: 700 },
    singleton: true,
    inDock: true,
    ...app(() => import("./browser/Browser")),
  },
  // Preview shows one file per window; the store's openFile opens it.
  preview: {
    id: "preview",
    name: "Preview",
    icon: "🖼️",
    size: { w: 720, h: 540 },
    ...app(() => import("./preview/Preview")),
  },
  // TextEdit edits one text file per window; opened with no file, it is a new
  // document.
  textedit: {
    id: "textedit",
    name: "TextEdit",
    icon: "📝",
    size: { w: 680, h: 520 },
    ...app(() => import("./textedit/TextEdit")),
  },
  // Activity Monitor: Processes, the CPU/Memory/Disk/Network graphs, the running
  // Agents, and Services & Ports. Opened from Spotlight; one is enough.
  activity: {
    id: "activity",
    name: "Activity Monitor",
    icon: "📊",
    size: { w: 820, h: 560 },
    singleton: true,
    ...app(() => import("./activity/ActivityMonitor")),
  },
  // Software: the installed Packages, the Install Ledger, Checkpoints and Replay
  // progress (PLAN.md §11). Opened from Spotlight; one is enough.
  software: {
    id: "software",
    name: "Software",
    icon: "📦",
    size: { w: 820, h: 560 },
    singleton: true,
    ...app(() => import("./software/Software")),
  },
  // System Settings: the Agent's model and limits, the API key, Protected Paths,
  // Memory, Trash, Keyboard, Appearance and Status (PLAN.md §4.3, M4.5). Opened
  // from the menu bar or Spotlight; one is enough.
  settings: {
    id: "settings",
    name: "System Settings",
    icon: "⚙️",
    size: { w: 820, h: 580 },
    singleton: true,
    ...app(() => import("./settings/Settings")),
  },
  // The Trash sits at the Dock's end, as on macOS.
  trash: {
    id: "trash",
    name: "Trash",
    icon: "🗑️",
    size: { w: 640, h: 420 },
    singleton: true,
    inDock: true,
    ...app(() => import("./trash/Trash")),
  },
};

// preloadApps fetches every app's chunk while nothing else is going on, newest
// Desktop first: the Agent app (Spotlight's "Ask the Agent" opens it and §16
// times the first step from that submit), then the Dock, then the rest.
export function preloadApps(info?: InfoResponse): void {
  const order: AppId[] = ["agent", "finder", "terminal", "preview", "textedit", "settings", "trash", "activity", "software", "about"];
  if (info?.browser) order.splice(3, 0, "browser");
  let i = 0;
  const next = () => {
    const id = order[i++];
    if (!id) return;
    void APPS[id].preload().catch(() => {});
    idle(next);
  };
  idle(next);
}

// idle runs f when the browser is not busy, falling back to a timeout on
// browsers without requestIdleCallback (Safari before 17).
function idle(f: () => void): void {
  const ric = (window as unknown as { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number }).requestIdleCallback;
  if (ric) ric(f, { timeout: 2000 });
  else setTimeout(f, 200);
}

export const DOCK_APPS: AppId[] = Object.values(APPS)
  .filter((a) => a.inDock)
  .map((a) => a.id);

// appShown says whether this Machine offers an app. The Browser is there only
// when the Machine asked for it (INCLUDE_BROWSER=true) — and then it is pinned
// in the Dock, even if the image lacks it, so opening it explains the rebuild.
export function appShown(id: AppId, info?: InfoResponse): boolean {
  return id !== "browser" || Boolean(info?.browser || info?.browserUnavailable);
}
