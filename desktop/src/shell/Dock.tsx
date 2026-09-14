import { useRef } from "react";

import { APPS, DOCK_APPS, type AppId } from "../apps/registry";
import { useDesktop } from "../store";

// Dock magnification radius and peak scale (PLAN.md §4.3): the tiles nearest the
// cursor grow. This is driven straight on the DOM in requestAnimationFrame so it
// never re-renders React (rule 1).
const RADIUS = 90;
const PEAK = 0.6;

export default function Dock() {
  const { openApp } = useDesktop();
  // Select the stable windows array, then derive the running set in render — a
  // selector that built a new Set each call would loop (never Object.is-equal).
  const windows = useDesktop((s) => s.windows);
  const running = new Set(windows.map((w) => w.appId));
  const ref = useRef<HTMLDivElement>(null);
  const frame = useRef(0);

  function magnify(clientX: number) {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      const dock = ref.current;
      if (!dock) return;
      for (const tile of Array.from(dock.children) as HTMLElement[]) {
        const box = tile.getBoundingClientRect();
        const dist = Math.abs(clientX - (box.left + box.width / 2));
        const scale = dist > RADIUS ? 1 : 1 + PEAK * (1 - dist / RADIUS);
        tile.style.transform = `scale(${scale})`;
      }
    });
  }

  function reset() {
    cancelAnimationFrame(frame.current);
    const dock = ref.current;
    if (!dock) return;
    for (const tile of Array.from(dock.children) as HTMLElement[]) tile.style.transform = "";
  }

  return (
    <div className="dock-wrap">
      <div className="dock" ref={ref} onPointerMove={(e) => magnify(e.clientX)} onPointerLeave={reset}>
        {DOCK_APPS.map((id) => (
          <DockTile key={id} id={id} running={running.has(id)} onOpen={() => openApp(id)} />
        ))}
      </div>
    </div>
  );
}

function DockTile({ id, running, onOpen }: { id: AppId; running: boolean; onOpen: () => void }) {
  const app = APPS[id];
  return (
    <button className="dock__tile" title={app.name} onClick={onOpen}>
      <span className="dock__icon">{app.icon}</span>
      <span className={`dock__dot${running ? " dock__dot--on" : ""}`} />
    </button>
  );
}
