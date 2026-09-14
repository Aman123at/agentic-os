// The Desktop's apps. Each is a lazily loaded chunk (PLAN.md §4.3 rule 3), so
// the shell stays small and an app's code is fetched only when it first opens.
import { lazy, type ComponentType, type LazyExoticComponent } from "react";

export type AppId = "about" | "finder" | "terminal";

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
};

export const DOCK_APPS: AppId[] = Object.values(APPS)
  .filter((a) => a.inDock)
  .map((a) => a.id);
