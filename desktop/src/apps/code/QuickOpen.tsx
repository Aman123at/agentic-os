// Quick Open (PLAN.md M8.7–M8.8): one palette over the window for going
// somewhere. What is typed picks the mode, as in VS Code:
//
//   name      a file of the Workspace, ranked by aosd (CodeService.FindFiles);
//             "name:42" opens it at line 42                              ⌘P
//   #name     a symbol anywhere in the Workspace (WorkspaceSymbols)        ⌘T
//   @name     a symbol in the file in front (DocumentSymbols)             ⌘⇧O
//   :42       line 42 of the file in front
//
// It also shows a ready-made list to choose from: several definitions of a
// name (F12), or its references (⇧F12). ⌘T is also the browser's New Tab,
// which a page cannot take, so ⌘P then # is the way there that always works.
import { Code as RpcCode, ConnectError } from "@connectrpc/connect";
import { useEffect, useRef, useState } from "react";

import { code } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { Symbol as CodeSymbol } from "../../gen/aos/v1/services_pb";
import { kindBadge } from "./navigate";
import { baseOf, relative } from "./paths";

// Reveal is a place in a file: a line, and optionally text on it to select;
// or an exact offset, where Back and Forward return to.
export interface Reveal {
  line?: number;
  find?: string;
  pos?: number;
}

export interface PaletteItem {
  key: string;
  label: string;
  hits?: number[]; // offsets into label to show in bold
  detail: string;
  badge?: string;
  path: string;
  at?: Reveal;
}

// A ready-made list, such as F12's several definitions.
export interface Preset {
  title: string;
  items: PaletteItem[];
}

// subsequence is where q's characters fall in text, in order and ignoring
// case, or null.
export function subsequence(q: string, text: string): number[] | null {
  const hits: number[] = [];
  const t = text.toLowerCase();
  let from = 0;
  for (const c of q.toLowerCase()) {
    if (c === " ") continue;
    const i = t.indexOf(c, from);
    if (i < 0) return null;
    hits.push(i);
    from = i + 1;
  }
  return hits;
}

export function symbolItem(s: CodeSymbol, root: string, q = ""): PaletteItem {
  return {
    key: `${s.path}:${s.line}:${s.name}`,
    label: s.name,
    hits: subsequence(q, s.name) ?? [],
    detail: `${s.scope ? `${s.scope} · ` : ""}${relative(s.path, root)}:${s.line}`,
    badge: kindBadge(s.kind),
    path: s.path,
    at: { line: s.line, find: s.name },
  };
}

type Mode = "files" | "symbols" | "file-symbols" | "line" | "preset";

function modeOf(value: string, preset?: Preset): { mode: Mode; query: string } {
  if (preset) return { mode: "preset", query: value.trim() };
  if (value.startsWith("#")) return { mode: "symbols", query: value.slice(1).trim() };
  if (value.startsWith("@")) return { mode: "file-symbols", query: value.slice(1).trim() };
  if (value.startsWith(":")) return { mode: "line", query: value.slice(1).trim() };
  return { mode: "files", query: value.trim() };
}

const PLACEHOLDER: Record<Mode, string> = {
  files: "Search files by name (:line for a line, # for symbols, @ for this file’s symbols)",
  symbols: "Search symbols in the Workspace",
  "file-symbols": "Search symbols in this file",
  line: "Type a line number",
  preset: "Filter",
};

export function QuickOpen({
  workspaceId,
  root,
  active,
  initial = "",
  preset,
  onOpen,
  onClose,
}: {
  workspaceId: string;
  root: string;
  active: string; // the file in front, or ""
  initial?: string;
  preset?: Preset;
  onOpen: (path: string, at?: Reveal) => void;
  onClose: () => void;
}) {
  const [value, setValue] = useState(initial);
  const [items, setItems] = useState<PaletteItem[] | null>(null);
  const [note, setNote] = useState("");
  const [sel, setSel] = useState(0);
  const seq = useRef(0);
  const list = useRef<HTMLDivElement>(null);
  // The file's symbols are fetched once and filtered here.
  const fileSymbols = useRef<CodeSymbol[] | null>(null);
  // What the items shown were found for, and an Enter pressed before the
  // items for what is typed now arrived: it opens the first of them.
  const shownFor = useRef("");
  const latest = useRef<PaletteItem[]>([]);
  const enterWaiting = useRef(false);

  const { mode, query } = modeOf(value, preset);
  const key = `${mode}\0${query}\0${active}`;
  useEffect(() => {
    const n = ++seq.current;
    const done = (next: PaletteItem[], why = "") => {
      if (n !== seq.current) return;
      setItems(next);
      setNote(why);
      setSel(0);
      shownFor.current = key;
      latest.current = next;
      if (enterWaiting.current) {
        enterWaiting.current = false;
        open(next[0]);
      }
    };
    const failed = (err: unknown) => {
      if (n !== seq.current) return;
      const e = ConnectError.from(err);
      setItems([]);
      setNote(e.code === RpcCode.FailedPrecondition ? e.rawMessage : friendlyError(err));
    };
    switch (mode) {
      case "preset": {
        const shown = preset!.items.flatMap((it) => {
          const hits = subsequence(query, it.label);
          return hits || subsequence(query, it.detail) ? [{ ...it, hits: query ? (hits ?? []) : it.hits }] : [];
        });
        return done(shown, shown.length ? "" : "Nothing matches.");
      }
      case "line": {
        const line = Number(query);
        if (!active) return done([], "Open a file first.");
        if (!query || !Number.isInteger(line) || line < 1) return done([], "Type a line number.");
        return done([{ key: "line", label: `Go to line ${line}`, detail: relative(active, root), path: active, at: { line } }]);
      }
      case "file-symbols": {
        if (!active) return done([], "Open a file first.");
        const show = (all: CodeSymbol[]) => {
          const shown = all.filter((s) => subsequence(query, s.name)).map((s) => symbolItem(s, root, query));
          done(shown, shown.length ? "" : all.length ? "No matching symbols." : "No symbols in this file.");
        };
        if (fileSymbols.current) return show(fileSymbols.current);
        setItems(null);
        code.documentSymbols({ workspaceId, path: active }).then((r) => {
          fileSymbols.current = r.symbols;
          show(r.symbols);
        }, failed);
        return;
      }
      case "symbols":
        if (!query) return done([], "Type part of a symbol’s name.");
        code.workspaceSymbols({ workspaceId, query, limit: 100, path: active }).then((r) => {
          const shown = r.symbols.map((s) => symbolItem(s, root, query));
          done(shown, shown.length ? (r.index?.partial ? partialNote(r.index.files, r.index.total) : "") : "No matching symbols.");
        }, failed);
        return;
      case "files": {
        const m = /^(.*?):(\d+)$/.exec(query);
        const name = m ? m[1] : query;
        const line = m ? Number(m[2]) : undefined;
        code.findFiles({ workspaceId, query: name, limit: 50 }).then((r) => {
          const shown = r.files.map((f): PaletteItem => {
            const rel = relative(f.path, root);
            const base = baseOf(rel);
            const dirLen = rel.length - base.length;
            return {
              key: f.path,
              label: base,
              hits: f.positions.filter((p) => p >= dirLen).map((p) => p - dirLen),
              detail: rel.slice(0, Math.max(0, dirLen - 1)),
              path: f.path,
              at: line ? { line } : undefined,
            };
          });
          done(shown, shown.length ? (r.truncated ? "Only the first 100 000 files are listed." : "") : name ? "No matching files." : "The folder has no files.");
        }, failed);
      }
    }
    // key is mode, query and active; open and the rest are read at the time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaceId, mode, query, active, preset]);

  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[data-i="${sel}"]`)?.scrollIntoView({ block: "nearest" });
  }, [sel]);

  const open = (it?: PaletteItem) => {
    if (!it) return;
    onClose();
    onOpen(it.path, it.at);
  };

  const shown = items ?? [];
  return (
    <div className="code-palette__scrim" onMouseDown={onClose}>
      <div className="code-palette" role="dialog" aria-label={preset ? preset.title : "Quick Open"} onMouseDown={(e) => e.stopPropagation()}>
        {preset && <div className="code-palette__title">{preset.title}</div>}
        <input
          className="code-palette__input"
          aria-label={preset ? "Filter" : "Go to"}
          placeholder={PLACEHOLDER[mode]}
          autoFocus
          spellCheck={false}
          value={value}
          onChange={(e) => {
            enterWaiting.current = false;
            setValue(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              const d = e.key === "ArrowDown" ? 1 : -1;
              setSel((s) => (shown.length ? (s + d + shown.length) % shown.length : 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              // Before the results for what is typed (or for the file, still
              // loading) are in, Enter waits for them.
              // The latest results, which may be a render ahead of the list.
              const now = latest.current;
              if (shownFor.current === key && now.length) open(now[now === items ? sel : 0]);
              else enterWaiting.current = true;
            } else if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            }
          }}
        />
        <div className="code-palette__list" role="listbox" aria-label="Places" ref={list}>
          {shown.map((it, i) => (
            <div
              key={it.key}
              data-i={i}
              role="option"
              aria-selected={i === sel}
              className={`code-palette__item${i === sel ? " code-palette__item--on" : ""}`}
              onMouseMove={() => setSel(i)}
              onClick={() => open(it)}
            >
              {it.badge && <span className="code-palette__badge">{it.badge}</span>}
              <span className="code-palette__name">{marked(it.label, it.hits)}</span>
              <span className="code-palette__dir">{it.detail}</span>
            </div>
          ))}
          {items === null && <div className="code-palette__note">{mode === "files" ? "Listing files…" : "Indexing symbols…"}</div>}
          {note && <div className="code-palette__note">{note}</div>}
        </div>
      </div>
    </div>
  );
}

export function partialNote(files: number, total: number): string {
  return `The Workspace has ${total.toLocaleString()} files, more than Code indexes (20 000), so only the folders of files you open are searched (${files.toLocaleString()} files so far).`;
}

// marked shows text with the characters at hits in bold.
function marked(text: string, hits?: number[]) {
  if (!hits?.length) return text;
  const hit = new Set(hits);
  const out: React.ReactNode[] = [];
  let run = "";
  let on = false;
  const flush = (k: number) => {
    if (run) out.push(on ? <b key={k}>{run}</b> : run);
    run = "";
  };
  for (let i = 0; i < text.length; i++) {
    if (hit.has(i) !== on) {
      flush(i);
      on = !on;
    }
    run += text[i];
  }
  flush(text.length);
  return out;
}
