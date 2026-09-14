import { Suspense, useEffect, useRef, type PointerEvent as ReactPointerEvent } from "react";

import { APPS } from "../apps/registry";
import { useDesktop, type Win } from "../store";

const MIN_W = 240;
const MIN_H = 160;

// Window is one app window. Dragging and resizing write transform/size straight
// to the DOM inside requestAnimationFrame and commit to the store once, on
// pointer-up (PLAN.md §4.3 rule 1), so a drag never triggers a React re-render.
export default function Window({ win }: { win: Win }) {
  const { focusWindow, setRect, closeWindow, minimize, toggleMaximize } = useDesktop();
  const focused = useDesktop((s) => s.focused === win.id);
  const app = APPS[win.appId];
  const ref = useRef<HTMLDivElement>(null);
  const drag = useRef<{ px: number; py: number; dx: number; dy: number } | null>(null);
  const resize = useRef<{ px: number; py: number; w: number; h: number } | null>(null);
  const frame = useRef(0);

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

  function onResizeStart(e: ReactPointerEvent) {
    e.stopPropagation();
    focusWindow(win.id);
    resize.current = { px: e.clientX, py: e.clientY, w: win.rect.w, h: win.rect.h };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onResizeMove(e: ReactPointerEvent) {
    const r = resize.current;
    if (!r) return;
    const w = Math.max(MIN_W, r.w + (e.clientX - r.px));
    const h = Math.max(MIN_H, r.h + (e.clientY - r.py));
    schedule(() => {
      if (ref.current) {
        ref.current.style.width = `${w}px`;
        ref.current.style.height = `${h}px`;
      }
    });
  }

  function onResizeEnd(e: ReactPointerEvent) {
    const r = resize.current;
    resize.current = null;
    if (!r) return;
    const w = Math.max(MIN_W, r.w + (e.clientX - r.px));
    const h = Math.max(MIN_H, r.h + (e.clientY - r.py));
    setRect(win.id, { ...win.rect, w, h });
  }

  function schedule(fn: () => void) {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(fn);
  }

  return (
    <section
      ref={ref}
      className={`window${focused ? " window--focused" : ""}`}
      style={{ left: win.rect.x, top: win.rect.y, width: win.rect.w, height: win.rect.h, zIndex: win.z, display: win.minimized ? "none" : undefined }}
      onPointerDown={() => focusWindow(win.id)}
      aria-label={win.title}
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
        <span className="window__title">{win.title}</span>
      </header>
      <div className="window__body">
        <Suspense fallback={<div className="placeholder">Loading…</div>}>
          <app.Component />
        </Suspense>
      </div>
      {!win.maximized && (
        <div
          className="window__resize"
          onPointerDown={onResizeStart}
          onPointerMove={onResizeMove}
          onPointerUp={onResizeEnd}
        />
      )}
    </section>
  );
}
