// Spotlight (PLAN.md §4.3, M3.4): the ⌥Space launcher. Type to find an app or a
// file, or press Enter to hand the whole query to the Agent as a new Task. It is
// deliberately small — a fuzzy command palette and richer results are M4.
import { useEffect, useMemo, useRef, useState } from "react";

import { files } from "../api/client";
import type { FileInfo } from "../gen/aos/v1/services_pb";
import { APPS, type AppDef, type AppId } from "../apps/registry";
import { PLACES, iconFor } from "../apps/finder/fs";
import { useDesktop } from "../store";

// The folders Spotlight searches (shallow): the sidebar Places except Trash.
const ROOTS = PLACES.filter((p) => p.path !== "");
// Apps offered by name.
const APP_LIST = Object.values(APPS);

type Result =
  | { kind: "app"; app: AppDef }
  | { kind: "file"; dir: string; file: FileInfo }
  | { kind: "task"; prompt: string };

interface Indexed {
  dir: string;
  file: FileInfo;
}

export default function Spotlight() {
  const open = useDesktop((s) => s.spotlight);
  const toggle = useDesktop((s) => s.toggleSpotlight);
  const openApp = useDesktop((s) => s.openApp);
  const createTask = useDesktop((s) => s.createTask);
  const revealInFinder = useDesktop((s) => s.revealInFinder);

  const [query, setQuery] = useState("");
  const [sel, setSel] = useState(0);
  const [index, setIndex] = useState<Indexed[]>([]);
  const input = useRef<HTMLInputElement>(null);

  // Reset and focus each time it opens; build a shallow file index once.
  useEffect(() => {
    if (!open) return;
    setQuery("");
    setSel(0);
    input.current?.focus();
    let cancelled = false;
    void (async () => {
      const found: Indexed[] = [];
      for (const root of ROOTS) {
        try {
          const resp = await files.list({ path: root.path });
          for (const file of resp.entries) found.push({ dir: root.path, file });
        } catch {
          // A missing or unreadable Place is simply not searched.
        }
      }
      if (!cancelled) setIndex(found);
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  const results = useMemo<Result[]>(() => {
    const q = query.trim().toLowerCase();
    const out: Result[] = [];
    for (const app of APP_LIST) {
      if (!q || app.name.toLowerCase().includes(q)) out.push({ kind: "app", app });
    }
    if (q) {
      for (const { dir, file } of index) {
        if (file.name.toLowerCase().includes(q)) out.push({ kind: "file", dir, file });
        if (out.length > 40) break;
      }
      out.push({ kind: "task", prompt: query.trim() });
    }
    return out;
  }, [query, index]);

  useEffect(() => {
    if (sel >= results.length) setSel(Math.max(0, results.length - 1));
  }, [results.length, sel]);

  if (!open) return null;

  function activate(r: Result | undefined) {
    if (!r) return;
    if (r.kind === "app") openApp(r.app.id as AppId);
    else if (r.kind === "file") revealInFinder(r.dir, r.file.path);
    else void createTask(r.prompt);
    toggle(false);
  }

  function onKey(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      toggle(false);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setSel((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSel((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      activate(results[sel]);
    }
  }

  return (
    <div className="spot-scrim" onClick={() => toggle(false)}>
      <div className="spot" onClick={(e) => e.stopPropagation()}>
        <input
          ref={input}
          className="spot__input"
          placeholder="Search apps and files, or ask the Agent…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKey}
          spellCheck={false}
          aria-label="Spotlight search"
        />
        {results.length > 0 && (
          <div className="spot__results">
            {results.map((r, i) => (
              <button
                key={rowKey(r, i)}
                className={`spot__row${i === sel ? " spot__row--on" : ""}`}
                onMouseEnter={() => setSel(i)}
                onClick={() => activate(r)}
              >
                <span className="spot__icon">{rowIcon(r)}</span>
                <span className="spot__label">{rowLabel(r)}</span>
                <span className="spot__kind">{rowKind(r)}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function rowKey(r: Result, i: number): string {
  if (r.kind === "app") return `app-${r.app.id}`;
  if (r.kind === "file") return `file-${r.file.path}`;
  return `task-${i}`;
}
function rowIcon(r: Result): string {
  if (r.kind === "app") return r.app.icon;
  if (r.kind === "file") return iconFor(r.file);
  return "🤖";
}
function rowLabel(r: Result): string {
  if (r.kind === "app") return r.app.name;
  if (r.kind === "file") return r.file.name;
  return `Ask the Agent: “${r.prompt}”`;
}
function rowKind(r: Result): string {
  if (r.kind === "app") return "App";
  if (r.kind === "file") return "File";
  return "New Task";
}
