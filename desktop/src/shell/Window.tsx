import { Suspense, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

import { APPS } from "../apps/registry";
import { useDesktop, type Win } from "../store";
import { Skeleton } from "../ui/Skeleton";
import { dockTileRect, type TileRect } from "./dockRect";
import { WinContext } from "./win";

const MIN_W = 240;
const MIN_H = 160;

// The eight directions a window resizes from: four edges and four corners, as on
// macOS (PLAN.md M4.8 item 8.10). Before M4.8 only "se" existed.
const DIRS = ["n", "s", "e", "w", "ne", "nw", "se", "sw"] as const;
type Dir = (typeof DIRS)[number];

// flyFrames are the keyframes for a window travelling between its place and its
// Dock tile: a translate to the tile's centre and a scale down to its size, with
// opacity fading out. transform-origin is the window's centre (the default), so
// the scale keeps the translated centre fixed. "out" plays it forwards, "in"
// reversed.
function flyFrames(rect: Win["rect"], tile: TileRect, dir: "in" | "out"): Keyframe[] {
  const dx = tile.cx - (rect.x + rect.w / 2);
  const dy = tile.cy - (rect.y + rect.h / 2);
  const scale = Math.min(tile.w / rect.w, tile.h / rect.h);
  const shown: Keyframe = { transform: "translate(0px, 0px) scale(1)", opacity: 1 };
  const stowed: Keyframe = { transform: `translate(${dx}px, ${dy}px) scale(${scale})`, opacity: 0 };
  return dir === "out" ? [shown, stowed] : [stowed, shown];
}

// resized is the rect a drag of (dx, dy) from `dir` produces. Dragging a north
// or west edge moves the opposite side's origin as well, and the minimum size
// pins the edge being dragged rather than shrinking past it.
function resized(r: Win["rect"], dir: Dir, dx: number, dy: number): Win["rect"] {
  const next = { ...r };
  if (dir.includes("e")) next.w = Math.max(MIN_W, r.w + dx);
  if (dir.includes("s")) next.h = Math.max(MIN_H, r.h + dy);
  if (dir.includes("w")) {
    next.w = Math.max(MIN_W, r.w - dx);
    next.x = r.x + (r.w - next.w);
  }
  if (dir.includes("n")) {
    next.h = Math.max(MIN_H, r.h - dy);
    next.y = r.y + (r.h - next.h);
  }
  return next;
}

// Window is one app window. Dragging and resizing write transform/size straight
// to the DOM inside requestAnimationFrame and commit to the store once, on
// pointer-up (PLAN.md §4.3 rule 1), so a drag never triggers a React re-render.
export default function Window({ win }: { win: Win }) {
  const { focusWindow, setRect, closeWindow, minimize, toggleMaximize } = useDesktop();
  const focused = useDesktop((s) => s.focused === win.id);
  const app = APPS[win.appId];
  // A document window is titled with its file's name, which Save As can change.
  const path = win.state?.path;
  const title = path ? path.slice(path.lastIndexOf("/") + 1) : win.title;
  const ref = useRef<HTMLDivElement>(null);
  const drag = useRef<{ px: number; py: number; dx: number; dy: number } | null>(null);
  const resize = useRef<{ px: number; py: number; dir: Dir } | null>(null);
  const frame = useRef(0);
  // `hidden` drives `display`, lagging win.minimized so the window is still on
  // screen while it animates to or from the Dock: display:none cannot be
  // animated. A window that mounts already minimized starts hidden and plays
  // nothing.
  const [hidden, setHidden] = useState(win.minimized);
  const flight = useRef<Animation | null>(null);

  // A gentle scale/fade in when the window first mounts (compositor-only).
  useEffect(() => {
    ref.current?.animate(
      [
        { opacity: 0, transform: "scale(0.96)" },
        { opacity: 1, transform: "scale(1)" },
      ],
      { duration: 140, easing: "ease-out" },
    );
  }, []);

  // Fly to the Dock tile on minimize, and back from it on restore. Both move
  // only transform and opacity (compositor-only, one rect read), so this stays
  // clear of the frame watchdog and the 60fps gate. If the state flips
  // mid-flight the running animation is cancelled — reverting to identity — and
  // the new direction takes over.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    flight.current?.cancel();
    flight.current = null;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    const tile = dockTileRect(app.name);
    if (win.minimized) {
      if (hidden) return; // already stowed
      if (reduce || !tile) {
        setHidden(true);
        return;
      }
      const a = el.animate(flyFrames(win.rect, tile, "out"), { duration: 220, easing: "ease-in", fill: "forwards" });
      flight.current = a;
      a.onfinish = () => setHidden(true); // keep the end state (fill) so there is no flash before display:none
    } else {
      if (!hidden) return; // already on screen (or an interrupted minimize snapped back)
      setHidden(false);
      if (reduce || !tile) return;
      const a = el.animate(flyFrames(win.rect, tile, "in"), { duration: 220, easing: "ease-out" });
      flight.current = a;
      a.onfinish = () => {
        if (flight.current === a) flight.current = null;
      };
    }
    // Only win.minimized should drive this; win.rect/app are read at flip time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [win.minimized]);

  function onDragStart(e: ReactPointerEvent) {
    if ((e.target as HTMLElement).closest(".traffic")) return;
    focusWindow(win.id);
    if (win.maximized) return;
    drag.current = { px: e.clientX, py: e.clientY, dx: 0, dy: 0 };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onDragMove(e: ReactPointerEvent) {
    const d = drag.current;
    if (!d) return;
    d.dx = e.clientX - d.px;
    d.dy = e.clientY - d.py;
    schedule(() => {
      if (ref.current) ref.current.style.transform = `translate3d(${d.dx}px, ${d.dy}px, 0)`;
    });
  }

  function onDragEnd() {
    const d = drag.current;
    drag.current = null;
    if (!d || !ref.current) return;
    ref.current.style.transform = "";
    if (d.dx || d.dy) setRect(win.id, { ...win.rect, x: win.rect.x + d.dx, y: win.rect.y + d.dy });
  }

  function onResizeStart(e: ReactPointerEvent, dir: Dir) {
    e.stopPropagation();
    focusWindow(win.id);
    resize.current = { px: e.clientX, py: e.clientY, dir };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onResizeMove(e: ReactPointerEvent) {
    const r = resize.current;
    if (!r) return;
    const next = resized(win.rect, r.dir, e.clientX - r.px, e.clientY - r.py);
    schedule(() => {
      const el = ref.current;
      if (!el) return;
      el.style.left = `${next.x}px`;
      el.style.top = `${next.y}px`;
      el.style.width = `${next.w}px`;
      el.style.height = `${next.h}px`;
    });
  }

  function onResizeEnd(e: ReactPointerEvent) {
    const r = resize.current;
    resize.current = null;
    if (!r) return;
    setRect(win.id, resized(win.rect, r.dir, e.clientX - r.px, e.clientY - r.py));
  }

  function schedule(fn: () => void) {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(fn);
  }

  return (
    <section
      ref={ref}
      className={`window${focused ? " window--focused" : ""}`}
      style={{ left: win.rect.x, top: win.rect.y, width: win.rect.w, height: win.rect.h, zIndex: win.z, display: hidden ? "none" : undefined }}
      onPointerDown={() => focusWindow(win.id)}
      aria-label={title}
    >
      <header
        className="window__bar"
        onPointerDown={onDragStart}
        onPointerMove={onDragMove}
        onPointerUp={onDragEnd}
        onDoubleClick={() => toggleMaximize(win.id)}
      >
        <div className="traffic">
          <button className="traffic__btn traffic__close" title="Close" onClick={() => closeWindow(win.id)} />
          <button className="traffic__btn traffic__min" title="Minimize" onClick={() => minimize(win.id)} />
          <button className="traffic__btn traffic__zoom" title="Zoom" onClick={() => toggleMaximize(win.id)} />
        </div>
        <span className="window__title">{title}</span>
      </header>
      <div className="window__body">
        <WinContext.Provider value={win.id}>
          <Suspense fallback={<Skeleton />}>
            <app.Component />
          </Suspense>
        </WinContext.Provider>
      </div>
      {!win.maximized &&
        DIRS.map((dir) => (
          <div
            key={dir}
            className={`window__resize window__resize--${dir}`}
            data-resize={dir}
            onPointerDown={(e) => onResizeStart(e, dir)}
            onPointerMove={onResizeMove}
            onPointerUp={onResizeEnd}
          />
        ))}
    </section>
  );
}
