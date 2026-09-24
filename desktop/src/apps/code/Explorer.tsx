// Code's Explorer (PLAN.md M8.3): the Workspace's folder as a tree, listed
// lazily through FileService as the Realm's user, and listed again when the
// Workspace's watch reports a change in a folder it shows. New File, New
// Folder, Rename and Move to Trash act through FileService too, so they are
// refused exactly where Finder's would be.
import { Code as RpcCode, ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import { files } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { FileInfo } from "../../gen/aos/v1/services_pb";
import { ContextMenu, MenuItem } from "../../ui/ContextMenu";
import { VirtualList } from "../../ui/VirtualList";
import { iconFor } from "../finder/fs";
import { dirOf, join } from "./paths";

const ROW = 22;

// Listed says which folders to list again: some, or every one shown.
export type Relist = (dirs: string[] | "all") => void;

type Listing = { entries: FileInfo[] } | { error: string };

// Editing is an inline name being typed: a new file or folder in dir, or a
// new name for path.
type Editing = { kind: "file" | "folder"; dir: string } | { kind: "rename"; dir: string; path: string; name: string };

type Row =
  | { kind: "entry"; info: FileInfo; depth: number }
  | { kind: "note"; text: string; depth: number; key: string }
  | { kind: "input"; depth: number; editing: Editing };

interface Props {
  root: string;
  active: string;
  // subscribe hands the Explorer a way to be told to list folders again.
  subscribe: (relist: Relist) => () => void;
  onOpen: (path: string) => void;
  onMoved: (from: string, to: string) => void;
  onError: (message: string) => void;
}

export function Explorer({ root, active, subscribe, onOpen, onMoved, onError }: Props) {
  const [listings, setListings] = useState<Map<string, Listing>>(() => new Map());
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([root]));
  const [editing, setEditing] = useState<Editing | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; info?: FileInfo } | null>(null);
  const loaded = useRef(listings);
  loaded.current = listings;
  const seq = useRef(new Map<string, number>());

  const list = useCallback(async (dir: string) => {
    const n = (seq.current.get(dir) ?? 0) + 1;
    seq.current.set(dir, n);
    let next: Listing;
    try {
      next = { entries: (await files.list({ path: dir })).entries };
    } catch (err) {
      next = { error: friendlyError(err) };
    }
    if (seq.current.get(dir) !== n) return; // a newer listing is on its way
    setListings((m) => new Map(m).set(dir, next));
  }, []);

  useEffect(() => {
    void list(root);
  }, [root, list]);

  // The watch's changes: list again each folder shown that changed.
  useEffect(
    () =>
      subscribe((dirs) => {
        const shown = [...loaded.current.keys()];
        const want = dirs === "all" ? shown : dirs.filter((d) => loaded.current.has(d));
        for (const d of new Set(want)) void list(d);
      }),
    [subscribe, list],
  );

  const toggle = (dir: string) => {
    setExpanded((s) => {
      const next = new Set(s);
      if (next.has(dir)) next.delete(dir);
      else {
        next.add(dir);
        if (!loaded.current.has(dir)) void list(dir);
      }
      return next;
    });
  };

  const rows = useMemo(() => {
    const out: Row[] = [];
    const walk = (dir: string, depth: number) => {
      if (editing && editing.kind !== "rename" && editing.dir === dir) out.push({ kind: "input", depth, editing });
      const l = listings.get(dir);
      if (!l) return void out.push({ kind: "note", text: "Loading…", depth, key: `loading:${dir}` });
      if ("error" in l) return void out.push({ kind: "note", text: l.error, depth, key: `error:${dir}` });
      if (l.entries.length === 0 && dir !== root) out.push({ kind: "note", text: "Empty", depth, key: `empty:${dir}` });
      for (const info of l.entries) {
        if (editing?.kind === "rename" && editing.path === info.path) out.push({ kind: "input", depth, editing });
        else out.push({ kind: "entry", info, depth });
        if (info.dir && expanded.has(info.path)) walk(info.path, depth + 1);
      }
    };
    walk(root, 0);
    return out;
  }, [listings, expanded, root, editing]);

  const startNew = (kind: "file" | "folder", dir: string) => {
    setMenu(null);
    setExpanded((s) => new Set(s).add(dir));
    if (!loaded.current.has(dir)) void list(dir);
    setEditing({ kind, dir });
  };

  const commit = async (e: Editing, name: string) => {
    setEditing(null);
    name = name.trim();
    if (!name || (e.kind === "rename" && name === e.name)) return;
    if (name.includes("/") || name === "." || name === "..") return onError(`“${name}” is not a name a file can have.`);
    const path = join(e.dir, name);
    try {
      if (e.kind === "file") {
        await files.write({ path, content: new Uint8Array(), overwrite: false });
        onOpen(path);
      } else if (e.kind === "folder") {
        await files.makeFolder({ path });
      } else if (e.kind === "rename") {
        await files.move({ source: e.path, destination: path, overwrite: false });
        onMoved(e.path, path);
      }
    } catch (err) {
      const c = ConnectError.from(err).code;
      onError(c === RpcCode.AlreadyExists ? `Something named “${name}” is already there.` : friendlyError(err));
    }
    void list(e.dir);
  };

  const trash = async (info: FileInfo) => {
    setMenu(null);
    try {
      await files.delete({ path: info.path });
    } catch (err) {
      onError(friendlyError(err));
    }
    void list(dirOf(info.path));
  };

  const name = root === "/" ? "/" : root.slice(root.lastIndexOf("/") + 1);
  return (
    <div className="code-explorer" aria-label="Explorer">
      <div className="code-explorer__head">
        <span className="code-explorer__title" title={root}>
          {name}
        </span>
        <button className="code__icon-btn" title="New File…" aria-label="New File" onClick={() => startNew("file", root)}>
          ＋
        </button>
        <button className="code__icon-btn" title="New Folder…" aria-label="New Folder" onClick={() => startNew("folder", root)}>
          ⊞
        </button>
        <button className="code__icon-btn" title="Refresh" aria-label="Refresh Explorer" onClick={() => [...loaded.current.keys()].forEach((d) => void list(d))}>
          ↻
        </button>
        <button className="code__icon-btn" title="Collapse Folders" aria-label="Collapse Folders" onClick={() => setExpanded(new Set([root]))}>
          ⊟
        </button>
      </div>
      <div
        className="code-explorer__body"
        role="tree"
        onContextMenu={(ev) => {
          ev.preventDefault();
          setMenu({ x: ev.clientX, y: ev.clientY });
        }}
      >
        <VirtualList className="code-explorer__tree" items={rows} rowHeight={ROW} renderRow={(row, style) => renderRow(row, style)} />
      </div>
      {menu && (
        <MenuScrim onClose={() => setMenu(null)}>
          <ContextMenu x={menu.x} y={menu.y}>
            {menu.info && !menu.info.dir && (
              <MenuItem
                label="Open"
                onClick={() => {
                  setMenu(null);
                  onOpen(menu.info!.path);
                }}
              />
            )}
            <MenuItem label="New File…" onClick={() => startNew("file", menu.info?.dir ? menu.info.path : menu.info ? dirOf(menu.info.path) : root)} />
            <MenuItem label="New Folder…" onClick={() => startNew("folder", menu.info?.dir ? menu.info.path : menu.info ? dirOf(menu.info.path) : root)} />
            {menu.info && (
              <>
                <div className="menu__sep" />
                <MenuItem
                  label="Rename…"
                  onClick={() => {
                    const info = menu.info!;
                    setMenu(null);
                    setEditing({ kind: "rename", dir: dirOf(info.path), path: info.path, name: info.name });
                  }}
                />
                <MenuItem label="Move to Trash" danger onClick={() => void trash(menu.info!)} />
              </>
            )}
          </ContextMenu>
        </MenuScrim>
      )}
    </div>
  );

  function renderRow(row: Row, style: CSSProperties) {
    const pad = { ...style, paddingLeft: 8 + row.depth * 14 };
    if (row.kind === "note") {
      return (
        <div key={row.key} className="code-explorer__note" style={{ ...pad, paddingLeft: 26 + row.depth * 14 }}>
          {row.text}
        </div>
      );
    }
    if (row.kind === "input") {
      const e = row.editing;
      return (
        <div key={`input:${e.kind}:${e.dir}`} className="code-explorer__row" style={pad}>
          <span className="code-explorer__icon">{e.kind === "folder" ? "📁" : "📄"}</span>
          <NameInput
            initial={e.kind === "rename" ? e.name : ""}
            label={e.kind === "file" ? "New file name" : e.kind === "folder" ? "New folder name" : "New name"}
            onDone={(v) => (v === null ? setEditing(null) : void commit(e, v))}
          />
        </div>
      );
    }
    const { info } = row;
    const open = info.dir && expanded.has(info.path);
    return (
      <div
        key={info.path}
        className={`code-explorer__row${info.path === active ? " code-explorer__row--on" : ""}`}
        style={pad}
        role="treeitem"
        aria-expanded={info.dir ? open : undefined}
        aria-selected={info.path === active}
        title={info.path}
        onClick={() => (info.dir ? toggle(info.path) : onOpen(info.path))}
        onContextMenu={(ev) => {
          ev.preventDefault();
          ev.stopPropagation();
          setMenu({ x: ev.clientX, y: ev.clientY, info });
        }}
      >
        <span className="code-explorer__twisty">{info.dir ? (open ? "▾" : "▸") : ""}</span>
        <span className="code-explorer__icon">{iconFor(info)}</span>
        <span className="code-explorer__name">{info.name}</span>
        {info.protected && (
          <span className="code-explorer__lock" title="Protected">
            🔒
          </span>
        )}
      </div>
    );
  }
}

// NameInput is the inline name field: Enter keeps it, Escape or leaving it
// without a name drops it.
function NameInput({ initial, label, onDone }: { initial: string; label: string; onDone: (v: string | null) => void }) {
  const [v, setV] = useState(initial);
  const done = useRef(false);
  const finish = (value: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(value);
  };
  return (
    <input
      className="code-explorer__input"
      aria-label={label}
      autoFocus
      value={v}
      spellCheck={false}
      onFocus={(e) => {
        const dot = initial.lastIndexOf(".");
        e.currentTarget.setSelectionRange(0, dot > 0 ? dot : initial.length);
      }}
      onChange={(e) => setV(e.target.value)}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") finish(v);
        else if (e.key === "Escape") finish(null);
      }}
      onBlur={() => finish(v.trim() ? v : null)}
    />
  );
}

// MenuScrim closes the context menu on the next click anywhere, or Escape.
function MenuScrim({ onClose, children }: { onClose: () => void; children: React.ReactNode }) {
  useEffect(() => {
    const close = () => onClose();
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("click", close);
    window.addEventListener("contextmenu", close, { capture: true });
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("contextmenu", close, { capture: true });
      window.removeEventListener("keydown", key);
    };
  }, [onClose]);
  return <>{children}</>;
}
