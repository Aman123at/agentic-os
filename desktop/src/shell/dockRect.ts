// dockTileRect finds where a window should fly when it minimizes: the centre and
// size of its app's Dock tile, in viewport coordinates. The Dock magnifies the
// tile under the pointer with an inline `transform: scale(n)` about its
// bottom-centre (see Dock.tsx and .dock__tile in index.css), and restore-from-
// dock happens with the pointer on that very tile — so the live rect is often
// enlarged. We divide the scale back out, keeping the bottom-centre fixed, so the
// animation always targets the tile's resting place.
export interface TileRect {
  cx: number;
  cy: number;
  w: number;
  h: number;
}

export function dockTileRect(appName: string): TileRect | null {
  const sel = `.dock__tile[title="${appName.replace(/["\\]/g, "\\$&")}"]`;
  const el = document.querySelector<HTMLElement>(sel);
  if (!el) return null;
  const box = el.getBoundingClientRect();
  const scale = scaleOf(el.style.transform);
  const w = box.width / scale;
  const h = box.height / scale;
  // transform-origin is bottom centre, so those two points do not move.
  const cx = box.left + box.width / 2;
  const bottom = box.bottom;
  return { cx, cy: bottom - h / 2, w, h };
}

// scaleOf reads the n from an inline `scale(n)` (Dock's magnification), or 1.
function scaleOf(transform: string): number {
  const m = /scale\(([\d.]+)\)/.exec(transform);
  const s = m ? parseFloat(m[1]) : 1;
  return s > 0 ? s : 1;
}
