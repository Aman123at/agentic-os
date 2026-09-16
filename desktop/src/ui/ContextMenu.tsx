// A right-click menu that opens at the pointer, but never off the screen: it
// flips above the pointer when it would run past the bottom and slides left when
// it would run past the right edge, the way a native menu does. It renders into
// the body so the window that opened it cannot clip it (PLAN.md M4.8 item 8.7).
// Promoted out of Finder so the Agent app and the Desktop share one menu.
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

export function ContextMenu({ x, y, children }: { x: number; y: number; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState({ x, y });

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const gap = 6;
    const { width, height } = el.getBoundingClientRect();
    const fitsBelow = y + height + gap <= window.innerHeight;
    setAt({
      x: Math.max(gap, Math.min(x, window.innerWidth - width - gap)),
      y: fitsBelow ? y : Math.max(gap, y - height),
    });
  }, [x, y, children]);

  return createPortal(
    <div ref={ref} className="menu" style={{ left: at.x, top: at.y }} onClick={(e) => e.stopPropagation()}>
      {children}
    </div>,
    document.body,
  );
}

export function MenuItem({ label, onClick, danger, disabled, title }: { label: string; onClick: () => void; danger?: boolean; disabled?: boolean; title?: string }) {
  return (
    <button className={`menu__item${danger ? " menu__item--danger" : ""}`} onClick={onClick} disabled={disabled} title={title}>
      {label}
    </button>
  );
}
