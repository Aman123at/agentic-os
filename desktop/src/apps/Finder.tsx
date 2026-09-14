// Finder browses the Machine's files over FileService, with the Trash over
// TrashService (PLAN.md §4.3, §13). Navigation, the sidebar and the two views
// live here; file actions (upload, download, move, delete, protect, Ask Agent)
// arrive in the next commit. Long folders are virtualised (§4.3 rule 5).
import { useCallback, useEffect, useRef, useState } from "react";

import { files, trash } from "../api/client";
import type { FileInfo, TrashItem } from "../gen/aos/v1/services_pb";
import { ConnectError } from "@connectrpc/connect";
import {
  PLACES,
  crumbs,
  decodeText,
  formatSize,
  formatWhen,
  iconFor,
  isImage,
  looksBinary,
  parent,
  readAll,
} from "./finder/fs";

type View = "list" | "icon";

const ROW_H = 28;
const TRASH = ""; // the Trash "folder" browses TrashService, not FileService

export default function Finder() {
  const [dir, setDir] = useState<string>("~");
  const [entries, setEntries] = useState<FileInfo[]>([]);
  const [trashItems, setTrashItems] = useState<TrashItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [view, setView] = useState<View>("list");
  const [selected, setSelected] = useState<string>("");
  const [quick, setQuick] = useState<FileInfo | null>(null);

  // Back/forward history of visited locations.
  const [history, setHistory] = useState<string[]>(["~"]);
  const [at, setAt] = useState(0);

  const load = useCallback(async (loc: string) => {
    setLoading(true);
    setError("");
    setSelected("");
    try {
      if (loc === TRASH) {
        const resp = await trash.listTrash({});
        setTrashItems(resp.items);
        setEntries([]);
      } else {
        const resp = await files.list({ path: loc });
        setEntries(resp.entries);
        setTrashItems([]);
      }
    } catch (err) {
      setError(ConnectError.from(err).message);
      setEntries([]);
      setTrashItems([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(dir);
  }, [dir, load]);

  // go navigates to a new location, pushing it onto the history.
  const go = useCallback(
    (loc: string) => {
      setHistory((h) => [...h.slice(0, at + 1), loc]);
      setAt((i) => i + 1);
      setDir(loc);
    },
    [at],
  );
  const back = useCallback(() => {
    if (at > 0) {
      setAt(at - 1);
      setDir(history[at - 1]);
    }
  }, [at, history]);
  const forward = useCallback(() => {
    if (at < history.length - 1) {
      setAt(at + 1);
      setDir(history[at + 1]);
    }
  }, [at, history]);

  const open = useCallback(
    (e: FileInfo) => {
      if (e.dir) go(e.path);
      else setQuick(e);
    },
    [go],
  );

  const up = parent(dir);
  const inTrash = dir === TRASH;

  return (
    <div className="finder">
      <nav className="finder__sidebar">
        <div className="finder__group">Places</div>
        {PLACES.map((p) => (
          <button
            key={p.id}
            className={`finder__place${dir === p.path ? " finder__place--on" : ""}`}
            onClick={() => go(p.path)}
          >
            <span className="finder__place-icon">{p.icon}</span>
            {p.name}
          </button>
        ))}
      </nav>

      <div className="finder__main">
        <div className="finder__toolbar">
          <div className="finder__nav">
            <button className="finder__btn" title="Back" disabled={at === 0} onClick={back}>
              ‹
            </button>
            <button className="finder__btn" title="Forward" disabled={at >= history.length - 1} onClick={forward}>
              ›
            </button>
            <button className="finder__btn" title="Up" disabled={inTrash || !up} onClick={() => up && go(up)}>
              ↑
            </button>
          </div>
          <div className="finder__crumbs">
            {inTrash ? (
              <span className="finder__crumb finder__crumb--on">🗑️ Trash</span>
            ) : (
              crumbs(dir).map((c, i, all) => (
                <span key={c.path}>
                  <button
                    className={`finder__crumb${i === all.length - 1 ? " finder__crumb--on" : ""}`}
                    onClick={() => go(c.path)}
                  >
                    {c.name}
                  </button>
                  {i < all.length - 1 && <span className="finder__sep">/</span>}
                </span>
              ))
            )}
          </div>
          <div className="finder__views">
            <button
              className={`finder__btn${view === "list" ? " finder__btn--on" : ""}`}
              title="List view"
              onClick={() => setView("list")}
            >
              ☰
            </button>
            <button
              className={`finder__btn${view === "icon" ? " finder__btn--on" : ""}`}
              title="Icon view"
              onClick={() => setView("icon")}
            >
              ▦
            </button>
          </div>
        </div>

        <div className="finder__body">
          {loading ? (
            <div className="finder__empty">Loading…</div>
          ) : error ? (
            <div className="finder__empty finder__empty--error">{error}</div>
          ) : inTrash ? (
            <TrashList items={trashItems} selected={selected} onSelect={setSelected} />
          ) : entries.length === 0 ? (
            <div className="finder__empty">This folder is empty.</div>
          ) : view === "list" ? (
            <FileList entries={entries} selected={selected} onSelect={setSelected} onOpen={open} />
          ) : (
            <IconGrid entries={entries} selected={selected} onSelect={setSelected} onOpen={open} />
          )}
        </div>

        <div className="finder__status">
          {inTrash
            ? `${trashItems.length} item${trashItems.length === 1 ? "" : "s"} in Trash`
            : `${entries.length} item${entries.length === 1 ? "" : "s"}`}
        </div>
      </div>

      {quick && <QuickLook file={quick} onClose={() => setQuick(null)} />}
    </div>
  );
}

// FileList is the virtualised list view: only the visible rows are rendered.
function FileList({
  entries,
  selected,
  onSelect,
  onOpen,
}: {
  entries: FileInfo[];
  selected: string;
  onSelect: (path: string) => void;
  onOpen: (e: FileInfo) => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState(0);
  const [height, setHeight] = useState(300);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    setHeight(el.clientHeight);
    const ro = new ResizeObserver(() => setHeight(el.clientHeight));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const first = Math.max(0, Math.floor(scroll / ROW_H) - 6);
  const count = Math.ceil(height / ROW_H) + 12;
  const slice = entries.slice(first, first + count);

  return (
    <div className="finder__list" ref={box} onScroll={(e) => setScroll(e.currentTarget.scrollTop)}>
      <div className="finder__list-head" style={{ top: scroll }}>
        <span className="finder__col-name">Name</span>
        <span className="finder__col-size">Size</span>
        <span className="finder__col-when">Modified</span>
      </div>
      <div style={{ height: entries.length * ROW_H, position: "relative" }}>
        {slice.map((e, i) => (
          <div
            key={e.path}
            className={`finder__row${selected === e.path ? " finder__row--on" : ""}`}
            style={{ top: (first + i) * ROW_H }}
            onClick={() => onSelect(e.path)}
            onDoubleClick={() => onOpen(e)}
          >
            <span className="finder__col-name">
              <span className="finder__icon">{iconFor(e)}</span>
              <span className="finder__name">{e.name}</span>
              {e.protected && <span className="finder__lock" title="Protected">🔒</span>}
            </span>
            <span className="finder__col-size">{formatSize(e.size, e.dir)}</span>
            <span className="finder__col-when">{formatWhen(e.modifiedAt)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function IconGrid({
  entries,
  selected,
  onSelect,
  onOpen,
}: {
  entries: FileInfo[];
  selected: string;
  onSelect: (path: string) => void;
  onOpen: (e: FileInfo) => void;
}) {
  return (
    <div className="finder__grid">
      {entries.map((e) => (
        <button
          key={e.path}
          className={`finder__tile${selected === e.path ? " finder__tile--on" : ""}`}
          onClick={() => onSelect(e.path)}
          onDoubleClick={() => onOpen(e)}
        >
          <span className="finder__tile-icon">{iconFor(e)}</span>
          <span className="finder__tile-name">{e.name}</span>
          {e.protected && <span className="finder__tile-lock" title="Protected">🔒</span>}
        </button>
      ))}
    </div>
  );
}

function TrashList({
  items,
  selected,
  onSelect,
}: {
  items: TrashItem[];
  selected: string;
  onSelect: (id: string) => void;
}) {
  if (items.length === 0) return <div className="finder__empty">The Trash is empty.</div>;
  return (
    <div className="finder__list">
      <div className="finder__list-head finder__list-head--static">
        <span className="finder__col-name">Original location</span>
        <span className="finder__col-size">Size</span>
        <span className="finder__col-when">Deleted</span>
      </div>
      {items.map((it) => (
        <div
          key={it.id}
          className={`finder__row finder__row--static${selected === it.id ? " finder__row--on" : ""}`}
          onClick={() => onSelect(it.id)}
        >
          <span className="finder__col-name">
            <span className="finder__icon">{it.dir ? "📁" : "📄"}</span>
            <span className="finder__name">{it.originalPath}</span>
          </span>
          <span className="finder__col-size">{formatSize(it.size, it.dir)}</span>
          <span className="finder__col-when">{formatWhen(it.deletedAt)}</span>
        </div>
      ))}
    </div>
  );
}

// QuickLook previews a file: images render inline, small text files show their
// contents, everything else offers a hint. Full Preview is M4.
function QuickLook({ file, onClose }: { file: FileInfo; onClose: () => void }) {
  const [state, setState] = useState<{ kind: "loading" | "image" | "text" | "none"; body?: string }>({ kind: "loading" });

  useEffect(() => {
    let url = "";
    let cancelled = false;
    (async () => {
      try {
        const bytes = await readAll(file.path);
        if (cancelled) return;
        if (isImage(file)) {
          url = URL.createObjectURL(new Blob([bytes as BlobPart]));
          setState({ kind: "image", body: url });
        } else if (!looksBinary(bytes)) {
          setState({ kind: "text", body: decodeText(bytes) });
        } else {
          setState({ kind: "none" });
        }
      } catch {
        if (!cancelled) setState({ kind: "none" });
      }
    })();
    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [file]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="quicklook" onClick={onClose}>
      <div className="quicklook__panel" onClick={(e) => e.stopPropagation()}>
        <header className="quicklook__bar">
          <span className="quicklook__title">
            {iconFor(file)} {file.name}
          </span>
          <button className="quicklook__close" title="Close" onClick={onClose}>
            ✕
          </button>
        </header>
        <div className="quicklook__body">
          {state.kind === "loading" && <div className="finder__empty">Loading…</div>}
          {state.kind === "image" && <img className="quicklook__image" src={state.body} alt={file.name} />}
          {state.kind === "text" && <pre className="quicklook__text">{state.body}</pre>}
          {state.kind === "none" && <div className="finder__empty">No preview available.</div>}
        </div>
      </div>
    </div>
  );
}
