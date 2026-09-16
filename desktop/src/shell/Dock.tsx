import { useCallback, useRef, useState } from "react";

import { appArt, trashFullArt } from "../assets";
import { APPS, DOCK_APPS, type AppId } from "../apps/registry";
import { useDesktop } from "../store";
import { DownloadsFan, DownloadsTile } from "./DownloadsStack";

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
  const trashCount = useDesktop((s) => s.trashCount);
  const ref = useRef<HTMLDivElement>(null);
  const frame = useRef(0);
  const [fan, setFan] = useState<DOMRect | null>(null);
  const closeFan = useCallback(() => setFan(null), []);

  function magnify(clientX: number) {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => {
      const dock = ref.current;
      if (!dock) return;
      for (const tile of Array.from(dock.children) as HTMLElement[]) {
        if (!tile.classList.contains("dock__tile")) continue;
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
        {DOCK_APPS.filter((id) => id !== "trash").map((id) => (
          <DockTile key={id} id={id} running={running.has(id)} onOpen={() => openApp(id)} />
        ))}
        {/* As on macOS, stacks and the Trash sit past a divider at the end. */}
        <span className="dock__divider" />
        <DownloadsTile
          open={fan !== null}
          onToggle={() => setFan((f) => (f ? null : (ref.current?.querySelector('.dock__tile[title="Downloads"]')?.getBoundingClientRect() ?? null)))}
        />
        <DockTile id="trash" running={running.has("trash")} onOpen={() => openApp("trash")} art={trashCount > 0 ? trashFullArt : undefined} />
      </div>
      {fan && <DownloadsFan anchor={fan} onClose={closeFan} />}
    </div>
  );
}

// A tile can override its art: the Trash shows a full bin while it has something
// in it, as on macOS — the picture is the signal, with no count (PLAN.md M4.8
// item 8.19). The tooltip stays the app's name, so the Dock reads the same
// whatever state a tile is in.
function DockTile({ id, running, onOpen, art: override }: { id: AppId; running: boolean; onOpen: () => void; art?: string }) {
  const app = APPS[id];
  const art = override ?? appArt[id];
  return (
    <button className="dock__tile" title={app.name} data-full={override ? "" : undefined} onClick={onOpen}>
      {art ? (
        <img className="dock__icon dock__icon--art" src={art} alt="" draggable={false} />
      ) : (
        <span className="dock__icon">{app.icon}</span>
      )}
      <span className={`dock__dot${running ? " dock__dot--on" : ""}`} />
    </button>
  );
}
