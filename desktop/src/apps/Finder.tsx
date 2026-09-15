// Finder browses the Machine's files over FileService, with the Trash over
// TrashService (PLAN.md §4.3, §13). Navigation and the list, icon and column
// views sit alongside the file actions: upload/download to the Host, move
// (drag-and-drop), Protect, move to Trash, restore/empty, and "Ask Agent…",
// which starts a Task. The open folder refreshes live through Watch (§15), and
// long folders are virtualised (§4.3 rule 5).
import { useCallback, useEffect, useRef, useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { files, trash } from "../api/client";
import { watchFolder } from "../api/watch";
import type { FileInfo, TrashItem } from "../gen/aos/v1/services_pb";
import { useWinFocused, useWinState } from "../shell/win";
import { useDesktop } from "../store";
import { Confirm } from "../ui/Confirm";
import { VirtualList } from "../ui/VirtualList";
import {
  PLACES,
  UploadError,
  basename,
  crumbs,
  downloadToHost,
  formatSize,
  formatWhen,
  iconFor,
  join,
  parent,
  uploadFile,
} from "./finder/fs";
import PreviewBody from "./preview/PreviewBody";

type View = "list" | "icon" | "column";

const ROW_H = 28;
const COL_ROW_H = 24;
const TRASH = ""; // the Trash "folder" browses TrashService, not FileService
const DRAG_TYPE = "application/x-aos-path"; // an internal move, told apart from a Host-file drop

interface Menu {
  x: number;
  y: number;
  file?: FileInfo;
  item?: TrashItem;
}

// trashOnly shows just the Trash, for the Trash app.
export default function Finder({ trashOnly = false }: { trashOnly?: boolean }) {
  // The folder and view are kept with the window, so a reload reopens them.
  const [savedDir, saveDir] = useWinState("dir", "~");
  const [savedView, saveView] = useWinState("view", "list");
  const [dir, setDir] = useState<string>(trashOnly ? TRASH : savedDir);
  const [entries, setEntries] = useState<FileInfo[]>([]);
  const [trashItems, setTrashItems] = useState<TrashItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [view, setView] = useState<View>(savedView === "icon" || savedView === "column" ? savedView : "list");
  const [selected, setSelected] = useState<string>("");
  const [quick, setQuick] = useState<FileInfo | null>(null);
  const [menu, setMenu] = useState<Menu | null>(null);
  const [ask, setAsk] = useState<FileInfo | null>(null);
  const [busy, setBusy] = useState("");
  const [dropping, setDropping] = useState(false);
  const [emptying, setEmptying] = useState(false);
  const upload = useRef<HTMLInputElement>(null);
  const revealRef = useRef<string>(""); // a path Spotlight asked us to select once loaded

  const [history, setHistory] = useState<string[]>([dir]);
  const [at, setAt] = useState(0);
  useEffect(() => {
    if (!trashOnly) saveDir(dir);
  }, [dir, saveDir, trashOnly]);
  useEffect(() => saveView(view), [view, saveView]);

  // Only the latest listing of the current folder may show: moving on before a
  // listing arrives (a reveal into a Finder that is still loading ~) must not let
  // the older, slower one land last.
  const loadSeq = useRef(0);
  const show = useCallback((listed: FileInfo[]) => {
    setEntries(listed);
    setTrashItems([]);
    setError("");
    setLoading(false);
    if (revealRef.current) {
      setSelected(revealRef.current);
      revealRef.current = "";
    }
  }, []);
  const load = useCallback(
    async (loc: string) => {
      const seq = ++loadSeq.current;
      const current = () => seq === loadSeq.current;
      try {
        if (loc === TRASH) {
          const items = (await trash.listTrash({})).items;
          if (!current()) return;
          setTrashItems(items);
          setEntries([]);
          setLoading(false);
        } else {
          const listed = (await files.list({ path: loc })).entries;
          if (current()) show(listed);
        }
      } catch (err) {
        if (!current()) return;
        setError(ConnectError.from(err).message);
        setEntries([]);
        setTrashItems([]);
        setLoading(false);
      }
    },
    [show],
  );

  // A folder follows the Machine through Watch; the Trash is listed when opened
  // and after each change made here.
  useEffect(() => {
    ++loadSeq.current;
    setLoading(true);
    setError("");
    setSelected("");
    if (dir === TRASH) {
      void load(dir);
      return;
    }
    return watchFolder(dir, {
      onEntries: (listed) => {
        ++loadSeq.current;
        show(listed);
      },
      onError: (err) => {
        setError(err.message);
        setEntries([]);
        setLoading(false);
      },
    });
  }, [dir, load, show]);
  const reload = useCallback(() => void load(dir), [dir, load]);

  // The Trash is not watched, so it is listed again whenever its window comes
  // to the front: files moved there from a Finder show up.
  const focused = useWinFocused();
  useEffect(() => {
    if (focused && dir === TRASH) reload();
  }, [focused, dir, reload]);

  const go = useCallback(
    (loc: string) => {
      setHistory((h) => [...h.slice(0, at + 1), loc]);
      setAt((i) => i + 1);
      setDir(loc);
    },
    [at],
  );
  const back = useCallback(() => at > 0 && (setAt(at - 1), setDir(history[at - 1])), [at, history]);
  const forward = useCallback(
    () => at < history.length - 1 && (setAt(at + 1), setDir(history[at + 1])),
    [at, history],
  );

  // Spotlight can ask the Finder to reveal a file: jump to its folder (or just
  // select it if we are already there) and clear the request.
  const finderJump = useDesktop((s) => s.finderJump);
  const clearFinderJump = useDesktop((s) => s.clearFinderJump);
  const reveal = useCallback(
    (folder: string, select: string) => {
      if (folder === dir) {
        setSelected(select);
      } else {
        revealRef.current = select;
        go(folder);
      }
    },
    [dir, go],
  );
  useEffect(() => {
    if (!finderJump) return;
    clearFinderJump();
    reveal(finderJump.dir, finderJump.select);
  }, [finderJump, reveal, clearFinderJump]);

  // Folders are entered by their logical path (so a Home-rooted walk stays
  // "~/…" rather than flipping to an absolute path); files open in their app.
  const openFile = useDesktop((s) => s.openFile);
  const open = useCallback((e: FileInfo) => (e.dir ? go(join(dir, e.name)) : openFile(e.path)), [go, dir, openFile]);

  // Space toggles Quick Look on the selected file while this window has the
  // focus, as on macOS. Typing in a field, and ⌥Space for Spotlight, pass through.
  useEffect(() => {
    if (!focused) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== " " || e.altKey || e.ctrlKey || e.metaKey || e.repeat) return;
      if ((e.target as HTMLElement | null)?.closest("input, textarea, select, [contenteditable]")) return;
      if (quick) {
        e.preventDefault();
        setQuick(null);
        return;
      }
      const file = entries.find((f) => f.path === selected);
      if (!file || file.dir || ask) return;
      e.preventDefault();
      setQuick(file);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [focused, quick, entries, selected, ask]);

  // run wraps a mutation: it reports failures in the status bar and reloads.
  const run = useCallback(
    async (label: string, fn: () => Promise<unknown>) => {
      setBusy(label);
      setError("");
      try {
        await fn();
        reload();
      } catch (err) {
        setError(ConnectError.from(err).message);
      } finally {
        setBusy("");
      }
    },
    [reload],
  );

  const doDownload = (e: FileInfo) => downloadToHost(e.path, e.name);
  const doProtect = (e: FileInfo) =>
    run(e.protected ? "Unprotecting…" : "Protecting…", () =>
      e.protected ? files.unprotect({ path: e.path }) : files.protect({ path: e.path }),
    );
  const doDelete = (e: FileInfo) => run(`Moving ${e.name} to Trash…`, () => files.delete({ path: e.path }));
  const doRestore = (it: TrashItem) => run("Restoring…", () => trash.restore({ id: it.id }));
  const doEmpty = () => {
    setEmptying(false);
    void run("Emptying Trash…", () => trash.empty({}));
  };
  const doMove = (srcPath: string, destDir: string) => {
    const dest = join(destDir, basename(srcPath));
    if (dest === srcPath) return;
    void run("Moving…", () => files.move({ source: srcPath, destination: dest }));
  };

  const uploadFiles = (list: FileList | null) => {
    if (!list || list.length === 0 || dir === TRASH) return;
    void run(`Uploading ${list.length} item${list.length === 1 ? "" : "s"}…`, async () => {
      for (const f of Array.from(list)) {
        const path = join(dir, f.name);
        try {
          await uploadFile(path, f, false);
        } catch (err) {
          if (!(err instanceof UploadError && err.status === 409)) throw err;
          if (!window.confirm(`${f.name} already exists here. Replace it?`)) continue;
          await uploadFile(path, f, true);
        }
      }
    });
  };

  const createTask = useDesktop((s) => s.createTask);
  const doAsk = (prompt: string) => {
    setAsk(null);
    void run("Starting Task…", () => createTask(prompt));
  };

  // act runs a context-menu choice and closes the menu.
  const act = (fn: () => void) => {
    setMenu(null);
    fn();
  };

  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  const up = parent(dir);
  const inTrash = dir === TRASH;
  // The active place is the most specific one containing the current folder, so
  // Home does not also light up while inside ~/Downloads.
  const onPlace = (p: string) => dir === p || (p !== "" && p !== "/" && dir.startsWith(`${p}/`));
  const activePlace = [...PLACES].filter((p) => onPlace(p.path)).sort((a, b) => b.path.length - a.path.length)[0];

  return (
    <div
      className={`finder${dropping ? " finder--drop" : ""}`}
      onDragOver={(e) => {
        if (!inTrash && e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDropping(true);
        }
      }}
      onDragLeave={(e) => {
        if (e.currentTarget === e.target) setDropping(false);
      }}
      onDrop={(e) => {
        if (!inTrash && e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDropping(false);
          uploadFiles(e.dataTransfer.files);
        }
      }}
    >
      {!trashOnly && (
        <nav className="finder__sidebar">
          <div className="finder__group">Places</div>
          {PLACES.map((p) => (
            <button
              key={p.id}
              className={`finder__place${p.id === activePlace?.id ? " finder__place--on" : ""}`}
              onClick={() => go(p.path)}
            >
              <span className="finder__place-icon">{p.icon}</span>
              {p.name}
            </button>
          ))}
        </nav>
      )}

      <div className="finder__main">
        <div className="finder__toolbar">
          {!trashOnly && (
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
          )}
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
                  {i < all.length - 1 && c.path !== "/" && <span className="finder__sep">/</span>}
                </span>
              ))
            )}
          </div>
          {inTrash ? (
            <button className="finder__btn" title="Empty the Trash" disabled={trashItems.length === 0} onClick={() => setEmptying(true)}>
              Empty
            </button>
          ) : (
            <button className="finder__btn" title="Upload from this computer" onClick={() => upload.current?.click()}>
              ⬆ Upload
            </button>
          )}
          {!inTrash && (
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
              <button
                className={`finder__btn${view === "column" ? " finder__btn--on" : ""}`}
                title="Column view"
                onClick={() => setView("column")}
              >
                ▥
              </button>
            </div>
          )}
        </div>

        <div className="finder__body">
          {view === "column" && !inTrash ? (
            // The columns stay up while the next folder loads, so walking down
            // a path does not flash.
            <ColumnView
              dir={dir}
              entries={loading || error ? [] : entries}
              selected={selected}
              onReveal={reveal}
              onOpen={open}
              onMenu={(x, y, file) => setMenu({ x, y, file })}
            />
          ) : loading ? (
            <div className="finder__empty">Loading…</div>
          ) : error ? (
            <div className="finder__empty finder__empty--error">{error}</div>
          ) : inTrash ? (
            <TrashList
              items={trashItems}
              selected={selected}
              onSelect={setSelected}
              onMenu={(x, y, item) => setMenu({ x, y, item })}
            />

          ) : entries.length === 0 ? (
            <div className="finder__empty">This folder is empty.</div>
          ) : view === "list" ? (
            <FileList
              entries={entries}
              selected={selected}
              onSelect={setSelected}
              onOpen={open}
              onMenu={(x, y, file) => setMenu({ x, y, file })}
              onMove={doMove}
            />
          ) : (
            <IconGrid
              entries={entries}
              selected={selected}
              onSelect={setSelected}
              onOpen={open}
              onMenu={(x, y, file) => setMenu({ x, y, file })}
              onMove={doMove}
            />
          )}
        </div>

        <div className="finder__status">
          {busy ||
            (view === "column" && error) ||
            (inTrash
              ? `${trashItems.length} item${trashItems.length === 1 ? "" : "s"} in Trash`
              : `${entries.length} item${entries.length === 1 ? "" : "s"}`)}
        </div>
      </div>

      <input
        ref={upload}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          uploadFiles(e.target.files);
          e.target.value = "";
        }}
      />

      {menu?.file && (
        <ContextMenu x={menu.x} y={menu.y}>
          {menu.file.dir ? (
            <MenuItem label="Open" onClick={() => act(() => open(menu.file!))} />
          ) : (
            <>
              <MenuItem label="Open" onClick={() => act(() => open(menu.file!))} />
              <MenuItem label="Quick Look" onClick={() => act(() => setQuick(menu.file!))} />
              <MenuItem label="Download…" onClick={() => act(() => doDownload(menu.file!))} />
            </>
          )}
          <MenuItem label={menu.file.protected ? "Unprotect" : "🔒 Protect"} onClick={() => act(() => doProtect(menu.file!))} />
          <MenuItem label="Ask Agent…" onClick={() => act(() => setAsk(menu.file!))} />
          <div className="menu__sep" />
          <MenuItem label="Move to Trash" danger onClick={() => act(() => doDelete(menu.file!))} />
        </ContextMenu>
      )}
      {menu?.item && (
        <ContextMenu x={menu.x} y={menu.y}>
          <MenuItem label="Put Back" onClick={() => act(() => doRestore(menu.item!))} />
        </ContextMenu>
      )}

      {emptying && (
        <Confirm
          title="Empty the Trash?"
          message={`This permanently deletes the ${trashItems.length} item${trashItems.length === 1 ? "" : "s"} in the Trash. It can't be undone.`}
          confirmLabel="Empty Trash"
          danger
          onConfirm={doEmpty}
          onCancel={() => setEmptying(false)}
        />
      )}
      {ask && <AskDialog file={ask} onCancel={() => setAsk(null)} onSubmit={doAsk} />}
      {quick && <QuickLook file={quick} onClose={() => setQuick(null)} />}
    </div>
  );
}

interface RowsProps {
  entries: FileInfo[];
  selected: string;
  onSelect: (path: string) => void;
  onOpen: (e: FileInfo) => void;
  onMenu: (x: number, y: number, file: FileInfo) => void;
  onMove: (src: string, destDir: string) => void;
}

// dragProps makes an entry a drag source, and a folder a drop target for moves.
function dragProps(e: FileInfo, onMove: RowsProps["onMove"]) {
  return {
    draggable: true,
    onDragStart: (ev: React.DragEvent) => {
      ev.dataTransfer.setData(DRAG_TYPE, e.path);
      ev.dataTransfer.effectAllowed = "move";
    },
    onDragOver: (ev: React.DragEvent) => {
      if (e.dir && ev.dataTransfer.types.includes(DRAG_TYPE)) {
        ev.preventDefault();
        ev.dataTransfer.dropEffect = "move";
      }
    },
    onDrop: (ev: React.DragEvent) => {
      const src = ev.dataTransfer.getData(DRAG_TYPE);
      if (e.dir && src) {
        ev.preventDefault();
        ev.stopPropagation();
        onMove(src, e.path);
      }
    },
  };
}

// FileList is the list view, virtualised: only the visible rows are rendered.
function FileList({ entries, selected, onSelect, onOpen, onMenu, onMove }: RowsProps) {
  return (
    <VirtualList
      className="finder__list"
      items={entries}
      rowHeight={ROW_H}
      header={
        <div className="finder__list-head">
          <span className="finder__col-name">Name</span>
          <span className="finder__col-size">Size</span>
          <span className="finder__col-when">Modified</span>
        </div>
      }
      renderRow={(e, style) => (
        <div
          key={e.path}
          className={`finder__row${selected === e.path ? " finder__row--on" : ""}`}
          style={style}
          onClick={() => onSelect(e.path)}
          onDoubleClick={() => onOpen(e)}
          onContextMenu={(ev) => {
            ev.preventDefault();
            onSelect(e.path);
            onMenu(ev.clientX, ev.clientY, e);
          }}
          {...dragProps(e, onMove)}
        >
          <span className="finder__col-name">
            <span className="finder__icon">{iconFor(e)}</span>
            <span className="finder__name">{e.name}</span>
            {e.protected && <span className="finder__lock" title="Protected">🔒</span>}
          </span>
          <span className="finder__col-size">{formatSize(e.size, e.dir)}</span>
          <span className="finder__col-when">{formatWhen(e.modifiedAt)}</span>
        </div>
      )}
    />
  );
}

function IconGrid({ entries, selected, onSelect, onOpen, onMenu, onMove }: RowsProps) {
  return (
    <div className="finder__grid">
      {entries.map((e) => (
        <button
          key={e.path}
          className={`finder__tile${selected === e.path ? " finder__tile--on" : ""}`}
          onClick={() => onSelect(e.path)}
          onDoubleClick={() => onOpen(e)}
          onContextMenu={(ev) => {
            ev.preventDefault();
            onSelect(e.path);
            onMenu(ev.clientX, ev.clientY, e);
          }}
          {...dragProps(e, onMove)}
        >
          <span className="finder__tile-icon">{iconFor(e)}</span>
          <span className="finder__tile-name">{e.name}</span>
          {e.protected && <span className="finder__tile-lock" title="Protected">🔒</span>}
        </button>
      ))}
    </div>
  );
}

// ColumnView shows each folder from the place's root down to the open one side
// by side, then the selected file. The open folder is the live listing; the
// folders above it are listed when the path changes. Clicking a folder opens it,
// and clicking a file selects it in its folder.
function ColumnView({
  dir,
  entries,
  selected,
  onReveal,
  onOpen,
  onMenu,
}: {
  dir: string;
  entries: FileInfo[];
  selected: string;
  onReveal: (dir: string, select: string) => void;
  onOpen: (e: FileInfo) => void;
  onMenu: (x: number, y: number, file: FileInfo) => void;
}) {
  const path = crumbs(dir);
  const box = useRef<HTMLDivElement>(null);
  const file = entries.find((e) => e.path === selected && !e.dir);

  // The open folder, and the file picked in it, stay in view.
  useEffect(() => {
    const el = box.current;
    if (el) el.scrollLeft = el.scrollWidth;
  }, [dir, file]);

  return (
    <div className="finder__columns" ref={box}>
      {path.map((c, i) => {
        const last = i === path.length - 1;
        const next = path[i + 1]?.name;
        return (
          <Column
            key={c.path}
            path={c.path}
            live={last ? entries : undefined}
            isOn={(e) => (last ? e.path === selected : e.name === next)}
            onClick={(e) => (e.dir ? onReveal(join(c.path, e.name), "") : onReveal(c.path, e.path))}
            onOpen={onOpen}
            onMenu={onMenu}
          />
        );
      })}
      {file && (
        <div className="finder__colpreview">
          <span className="finder__colpreview-icon">{iconFor(file)}</span>
          <span className="finder__colpreview-name">{file.name}</span>
          <span className="finder__colpreview-meta">
            {formatSize(file.size, false)} · {formatWhen(file.modifiedAt)}
          </span>
          <button className="finder__btn" onClick={() => onOpen(file)}>
            Open
          </button>
        </div>
      )}
    </div>
  );
}

function Column({
  path,
  live,
  isOn,
  onClick,
  onOpen,
  onMenu,
}: {
  path: string;
  live?: FileInfo[];
  isOn: (e: FileInfo) => boolean;
  onClick: (e: FileInfo) => void;
  onOpen: (e: FileInfo) => void;
  onMenu: (x: number, y: number, file: FileInfo) => void;
}) {
  const [listed, setListed] = useState<FileInfo[]>([]);
  useEffect(() => {
    if (live) return;
    let gone = false;
    files.list({ path }).then(
      (r) => !gone && setListed(r.entries),
      () => !gone && setListed([]),
    );
    return () => {
      gone = true;
    };
  }, [path, live]);
  const items = live ?? listed;
  return (
    <VirtualList
      className="finder__column"
      items={items}
      rowHeight={COL_ROW_H}
      renderRow={(e, style) => (
        <div
          key={e.path}
          style={style}
          className={`finder__colrow${isOn(e) ? " finder__colrow--on" : ""}`}
          onClick={() => onClick(e)}
          onDoubleClick={() => !e.dir && onOpen(e)}
          onContextMenu={(ev) => {
            ev.preventDefault();
            onMenu(ev.clientX, ev.clientY, e);
          }}
        >
          <span className="finder__icon">{iconFor(e)}</span>
          <span className="finder__name">{e.name}</span>
          {e.protected && <span className="finder__lock" title="Protected">🔒</span>}
          {e.dir && <span className="finder__colchev">›</span>}
        </div>
      )}
    />
  );
}

function TrashList({
  items,
  selected,
  onSelect,
  onMenu,
}: {
  items: TrashItem[];
  selected: string;
  onSelect: (id: string) => void;
  onMenu: (x: number, y: number, item: TrashItem) => void;
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
          onContextMenu={(ev) => {
            ev.preventDefault();
            onSelect(it.id);
            onMenu(ev.clientX, ev.clientY, it);
          }}
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

function ContextMenu({ x, y, children }: { x: number; y: number; children: React.ReactNode }) {
  return (
    <div className="menu" style={{ left: x, top: y }} onClick={(e) => e.stopPropagation()}>
      {children}
    </div>
  );
}

function MenuItem({ label, onClick, danger }: { label: string; onClick: () => void; danger?: boolean }) {
  return (
    <button className={`menu__item${danger ? " menu__item--danger" : ""}`} onClick={onClick}>
      {label}
    </button>
  );
}

// AskDialog collects a request and starts a Task that references the file; the
// Agent app opens on it.
function AskDialog({ file, onCancel, onSubmit }: { file: FileInfo; onCancel: () => void; onSubmit: (prompt: string) => void }) {
  const [text, setText] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => ref.current?.focus(), []);
  const submit = () => {
    const t = text.trim();
    if (t) onSubmit(`${t}\n\nFile: ${file.path}`);
  };
  return (
    <div className="quicklook" onClick={onCancel}>
      <div className="ask" onClick={(e) => e.stopPropagation()}>
        <h3 className="ask__title">Ask the Agent about {file.name}</h3>
        <textarea
          ref={ref}
          className="ask__text"
          aria-label="What should the Agent do?"
          placeholder="e.g. Summarise this file, or rename it to something clearer"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
          }}
        />
        <div className="ask__actions">
          <button className="finder__btn" onClick={onCancel}>
            Cancel
          </button>
          <button className="finder__btn finder__btn--on" disabled={!text.trim()} onClick={submit}>
            Start Task
          </button>
        </div>
      </div>
    </div>
  );
}

// QuickLook previews a file inside the Finder, through the same view Preview uses.
function QuickLook({ file, onClose }: { file: FileInfo; onClose: () => void }) {
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
          <PreviewBody path={file.path} compact />
        </div>
      </div>
    </div>
  );
}
