// The Desktop's apps. Each is a lazily loaded chunk (PLAN.md §4.3 rule 3), so
// the shell stays small and an app's code is fetched only when it first opens.
import { lazy, type ComponentType, type LazyExoticComponent } from "react";

export type AppId = "about" | "finder" | "terminal" | "agent" | "preview" | "textedit" | "trash" | "activity" | "software" | "settings";

export interface AppDef {
  id: AppId;
  name: string;
  icon: string; // an emoji for now; the drawn SVG icon set arrives in M3.5
  size: { w: number; h: number };
  singleton?: boolean;
  inDock?: boolean;
  Component: LazyExoticComponent<ComponentType>;
}

export const APPS: Record<AppId, AppDef> = {
  about: {
    id: "about",
    name: "About This Machine",
    icon: "🖥️",
    size: { w: 380, h: 300 },
    singleton: true,
    Component: lazy(() => import("./About")),
  },
  finder: {
    id: "finder",
    name: "Finder",
    icon: "🗂️",
    size: { w: 760, h: 480 },
    inDock: true,
    Component: lazy(() => import("./Finder")),
  },
  terminal: {
    id: "terminal",
    name: "Terminal",
    icon: "⌨️",
    size: { w: 660, h: 420 },
    inDock: true,
    Component: lazy(() => import("./Terminal")),
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
    Component: lazy(() => import("./agent/Agent")),
  },
  // Preview shows one file per window; the store's openFile opens it.
  preview: {
    id: "preview",
    name: "Preview",
    icon: "🖼️",
    size: { w: 720, h: 540 },
    Component: lazy(() => import("./preview/Preview")),
  },
  // TextEdit edits one text file per window; opened with no file, it is a new
  // document.
  textedit: {
    id: "textedit",
    name: "TextEdit",
    icon: "📝",
    size: { w: 680, h: 520 },
    Component: lazy(() => import("./textedit/TextEdit")),
  },
  // Activity Monitor: Processes, the CPU/Memory/Disk/Network graphs, the running
  // Agents, and Services & Ports. Opened from Spotlight; one is enough.
  activity: {
    id: "activity",
    name: "Activity Monitor",
    icon: "📊",
    size: { w: 820, h: 560 },
    singleton: true,
    Component: lazy(() => import("./activity/ActivityMonitor")),
  },
  // Software: the installed Packages, the Install Ledger, Checkpoints and Replay
  // progress (PLAN.md §11). Opened from Spotlight; one is enough.
  software: {
    id: "software",
    name: "Software",
    icon: "📦",
    size: { w: 820, h: 560 },
    singleton: true,
    Component: lazy(() => import("./software/Software")),
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
    Component: lazy(() => import("./settings/Settings")),
  },
  // The Trash sits at the Dock's end, as on macOS.
  trash: {
    id: "trash",
    name: "Trash",
    icon: "🗑️",
    size: { w: 640, h: 420 },
    singleton: true,
    inDock: true,
    Component: lazy(() => import("./trash/Trash")),
  },
};

export const DOCK_APPS: AppId[] = Object.values(APPS)
  .filter((a) => a.inDock)
  .map((a) => a.id);
