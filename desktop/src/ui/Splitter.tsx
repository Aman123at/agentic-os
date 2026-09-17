// The draggable divider between two side-by-side panes. It sits between them in
// the flex row and sizes the pane *before* it: the Agent app's view sidebar and
// Task list, and Finder's Places sidebar.
//
// A drag writes the pane's width straight to the DOM inside
// requestAnimationFrame and commits to the caller once, on pointer-up (PLAN.md
// §4.3 rule 1) — the same shape as a window drag in shell/Window.tsx — so
// pulling a divider never re-renders React and stays clear of the frame
// watchdog and the 60fps gate.
//
// It is a real separator to the keyboard and to a screen reader: ← and → nudge
// it, Home and End run it to its limits, and Enter collapses or restores the
// pane. Dragging well past the minimum collapses too, as it does on macOS; a
// collapsed divider shows a chevron and a single click brings the pane back.
import { useRef, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";

// One arrow-key nudge, and how far past the minimum a drag has to go before the
// pane collapses instead of sticking at its minimum.
const STEP = 16;
const COLLAPSE = 28;

export function Splitter({
  size,
  onSize,
  min,
  max,
  label,
  restore = min,
}: {
  // The pane's current width in pixels; 0 means collapsed.
  size: number;
  onSize: (size: number) => void;
  min: number;
  max: number;
  // Names the pane this divider sizes, e.g. "Resize the Tasks list".
  label: string;
  // The width a collapsed pane comes back to when nothing better is remembered.
  restore?: number;
}) {
  const el = useRef<HTMLDivElement>(null);
  const drag = useRef<{ px: number; from: number; at: number } | null>(null);
  const frame = useRef(0);
  // The width to come back to after a collapse. A reload that lands collapsed
  // has nothing to remember, and falls back to `restore`.
  const last = useRef(size || restore);

  // The pane being sized is the element in front of the divider. Reaching for
  // it beats threading a ref through every caller, and keeps the two in step by
  // construction: the divider can only ever size the pane it is next to.
  const pane = () => el.current?.previousElementSibling as HTMLElement | null;

  function clamp(w: number) {
    if (w < min - COLLAPSE) return 0;
    return Math.max(min, Math.min(max, w));
  }

  function paint(w: number) {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      const p = pane();
      if (p) p.style.width = `${w}px`;
    });
  }

  function commit(w: number) {
    if (w > 0) last.current = w;
    onSize(w);
  }

  function onPointerDown(e: ReactPointerEvent) {
    // A collapsed divider is a handle: clicking it brings the pane back.
    if (size === 0) {
      commit(last.current || restore);
      return;
    }
    drag.current = { px: e.clientX, from: size, at: size };
    e.currentTarget.setPointerCapture(e.pointerId);
    el.current?.classList.add("split--live");
  }

  function onPointerMove(e: ReactPointerEvent) {
    const d = drag.current;
    if (!d) return;
    d.at = clamp(d.from + e.clientX - d.px);
    paint(d.at);
  }

  function onPointerUp() {
    const d = drag.current;
    drag.current = null;
    el.current?.classList.remove("split--live");
    if (!d) return;
    cancelAnimationFrame(frame.current);
    // React owns the width again; drop the inline one written during the drag.
    const p = pane();
    if (p) p.style.width = "";
    if (d.at !== d.from) commit(d.at);
  }

  function toggle() {
    commit(size === 0 ? last.current || restore : 0);
  }

  function onKeyDown(e: ReactKeyboardEvent) {
    const keys: Record<string, number> = {
      ArrowLeft: Math.max(min, size - STEP),
      ArrowRight: size === 0 ? min : Math.min(max, size + STEP),
      Home: min,
      End: max,
    };
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      toggle();
    } else if (e.key in keys) {
      e.preventDefault();
      commit(keys[e.key]);
    }
  }

  return (
    <div
      ref={el}
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={size}
      aria-valuemin={0}
      aria-valuemax={max}
      tabIndex={0}
      className={`split${size === 0 ? " split--off" : ""}`}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={toggle}
      onKeyDown={onKeyDown}
    >
      <span className="split__grip" aria-hidden="true" />
    </div>
  );
}
