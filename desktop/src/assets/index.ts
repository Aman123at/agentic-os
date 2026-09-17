// The Aurora asset set (PLAN.md §4.3): the Desktop's original app icons and
// gradient wallpapers, drawn for the project and described in manifest.json.
// Vite turns each `?url` import into a hashed asset the browser fetches at
// runtime, so the SVGs stay out of the shell's initial JS bundle. To swap the
// set, replace the files here (and manifest.json) — nothing else references the
// paths directly.
import type { AppId } from "../apps/registry";
import aboutIcon from "./about.svg?url";
import agentIcon from "./agent.svg?url";
import browserIcon from "./browser.svg?url";
import downloadsIcon from "./downloads.svg?url";
import finderIcon from "./finder.svg?url";
import previewIcon from "./preview.svg?url";
import terminalIcon from "./terminal.svg?url";
import texteditIcon from "./textedit.svg?url";
import trashIcon from "./trash.svg?url";
import trashFullIcon from "./trash-full.svg?url";
import wallpaperDark from "./wallpaper-dark.svg?url";
import wallpaperLight from "./wallpaper-light.svg?url";

export const wallpapers: Record<"light" | "dark", string> = {
  light: wallpaperLight,
  dark: wallpaperDark,
};

// The drawn icon for each app; apps without one fall back to their emoji.
export const appArt: Partial<Record<AppId, string>> = {
  finder: finderIcon,
  terminal: terminalIcon,
  agent: agentIcon,
  browser: browserIcon,
  about: aboutIcon,
  preview: previewIcon,
  textedit: texteditIcon,
  trash: trashIcon,
};

// The Trash tile swaps to this while the Trash has something in it, the way
// macOS shows a full bin (PLAN.md M4.8 item 8.19).
export const trashFullArt = trashFullIcon;

// The Dock's Downloads stack, which is not an app.
export const downloadsArt = downloadsIcon;
