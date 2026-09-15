// The Downloads stack (PLAN.md §4.3): a Dock tile that fans out the newest files
// in ~/Downloads above it, with downloads still in progress (DownloadProgress
// events) on top. The folder is watched only while the fan is open.
import { Code } from "@connectrpc/connect";
import { useEffect, useMemo, useRef, useState } from "react";

import { watchFolder } from "../api/watch";
import { downloadsArt } from "../assets";
import type { FileInfo } from "../gen/aos/v1/services_pb";
import { formatSize, iconFor } from "../apps/finder/fs";
import { useDesktop } from "../store";

const FOLDER = "~/Downloads";
const SHOWN = 12;

export function DownloadsTile({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  const active = useDesktop((s) => Object.keys(s.downloads).length);
  return (
    <button className={`dock__tile${open ? " dock__tile--open" : ""}`} title="Downloads" aria-expanded={open} onClick={onToggle}>
      <img className="dock__icon dock__icon--art" src={downloadsArt} alt="" draggable={false} />
      {active > 0 && (
        <span className="dock__badge" aria-label={`${active} downloading`}>
          {active}
        </span>
      )}
      <span className="dock__dot" />
    </button>
  );
}

export function DownloadsFan({ anchor, onClose }: { anchor: DOMRect; onClose: () => void }) {
  const downloads = useDesktop((s) => s.downloads);
  const openFile = useDesktop((s) => s.openFile);
  const revealInFinder = useDesktop((s) => s.revealInFinder);
  const [entries, setEntries] = useState<FileInfo[] | null>(null);
  const [error, setError] = useState("");
  const box = useRef<HTMLDivElement>(null);

  useEffect(
    () =>
      watchFolder(FOLDER, {
        onEntries: (e) => {
          setEntries(e);
          setError("");
        },
        // No ~/Downloads yet just means nothing was downloaded.
        onError: (err) => {
          if (err.code === Code.NotFound) setEntries([]);
          else setError(err.message);
        },
      }),
    [],
  );

  // A click anywhere else, or Escape, folds the fan away.
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      const t = e.target as Element | null;
      if (!box.current?.contains(t) && !t?.closest('.dock__tile[title="Downloads"]')) onClose();
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  const recent = useMemo(
    () =>
      (entries ?? [])
        .filter((e) => !e.name.startsWith("."))
        .sort((a, b) => Number((b.modifiedAt?.seconds ?? 0n) - (a.modifiedAt?.seconds ?? 0n)))
        .slice(0, SHOWN),
    [entries],
  );
  const inProgress = Object.values(downloads);

  return (
    <div
      ref={box}
      className="stack"
      role="dialog"
      aria-label="Downloads"
      style={{ left: anchor.left + anchor.width / 2, bottom: window.innerHeight - anchor.top + 12 }}
    >
      {inProgress.map((d) => {
        const name = d.path.slice(d.path.lastIndexOf("/") + 1) || d.url;
        const pct = d.total > 0n ? Math.min(100, Number((d.bytes * 100n) / d.total)) : null;
        return (
          <div key={d.stepId} className="stack__item stack__item--busy" title={d.url}>
            <span className="stack__icon">⏬</span>
            <span className="stack__text">
              <span className="stack__name">{name}</span>
              <span className="stack__meta">
                {formatSize(d.bytes, false)}
                {d.total > 0n && ` of ${formatSize(d.total, false)}`}
              </span>
              <span className="stack__bar" role="progressbar" aria-label={`Downloading ${name}`} aria-valuenow={pct ?? undefined}>
                <span className={`stack__fill${pct === null ? " stack__fill--unknown" : ""}`} style={{ width: `${pct ?? 30}%` }} />
              </span>
            </span>
          </div>
        );
      })}
      {entries === null && !error && <div className="stack__note">Loading…</div>}
      {error && <div className="stack__note">{error}</div>}
      {entries !== null && recent.length === 0 && inProgress.length === 0 && <div className="stack__note">Nothing downloaded yet.</div>}
      {recent.map((e) => (
        <button
          key={e.path}
          className="stack__item"
          title={e.path}
          onClick={() => {
            onClose();
            if (e.dir) revealInFinder(e.path, "");
            else openFile(e.path);
          }}
        >
          <span className="stack__icon">{iconFor(e)}</span>
          <span className="stack__text">
            <span className="stack__name">{e.name}</span>
            <span className="stack__meta">{e.dir ? "Folder" : formatSize(e.size, false)}</span>
          </span>
        </button>
      ))}
      <button
        className="stack__folder"
        onClick={() => {
          onClose();
          revealInFinder(FOLDER, "");
        }}
      >
        Open in Finder
      </button>
    </div>
  );
}
