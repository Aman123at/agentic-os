// The generated wallpapers (PLAN.md §4.3): a small set of designs, each a layered
// CSS gradient tuned by a hue and a saturation. The gradients themselves live in
// index.css, keyed by `data-design`, and read the hue and saturation from the CSS
// custom properties this module supplies — so dragging the sliders re-paints on
// the compositor without a React render, and each design darkens itself under a
// dark theme through the same `[data-theme]` rules the Aurora art uses.
import type { CSSProperties } from "react";

export const WALLPAPER_DESIGNS = [
  { id: "mesh", name: "Mesh" },
  { id: "dunes", name: "Dunes" },
  { id: "halo", name: "Halo" },
  { id: "ridge", name: "Ridge" },
  { id: "nebula", name: "Nebula" },
  { id: "tide", name: "Tide" },
] as const;

export type WallpaperDesignId = (typeof WALLPAPER_DESIGNS)[number]["id"];

// A fresh design starts at a calm indigo. The sliders move from here.
export const DEFAULT_HUE = 255;
export const DEFAULT_SAT = 62;

export function isDesignId(id: string): id is WallpaperDesignId {
  return WALLPAPER_DESIGNS.some((d) => d.id === id);
}

// wallpaperVars is the inline style a generated wallpaper (or a swatch of one)
// carries: the hue and saturation the CSS reads. clamp keeps a restored blob or
// a stray slider value in range.
export function wallpaperVars(hue: number, sat: number): CSSProperties {
  return { "--wall-hue": clamp(hue, 0, 360), "--wall-sat": `${clamp(sat, 0, 100)}%` } as CSSProperties;
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, Math.round(Number.isFinite(n) ? n : lo)));
}
