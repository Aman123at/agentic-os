// Quick Open (PLAN.md M8.7): ⌘P, a file of the Workspace by a few of its
// letters. aosd ranks the Workspace's files (CodeService.FindFiles), which it
// lists once as the Realm's user, honouring .gitignore, and keeps current from
// the watch. "name:42" opens the file at line 42.
import { useEffect, useRef, useState } from "react";

import { code } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { FoundFile } from "../../gen/aos/v1/services_pb";
import { baseOf } from "./paths";

// A query and an optional line: "main.go:12".
function parse(q: string): { query: string; line?: number } {
  const m = /^(.*?):(\d+)$/.exec(q.trim());
  return m ? { query: m[1], line: Number(m[2]) } : { query: q.trim() };
}

export function QuickOpen({
  workspaceId,
  root,
  onOpen,
  onClose,
}: {
  workspaceId: string;
  root: string;
  onOpen: (path: string, line?: number) => void;
  onClose: () => void;
}) {
  const [value, setValue] = useState("");
  const [found, setFound] = useState<FoundFile[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState("");
  const [sel, setSel] = useState(0);
  const seq = useRef(0);
  const list = useRef<HTMLDivElement>(null);

  const { query, line } = parse(value);
  useEffect(() => {
    const n = ++seq.current;
    code.findFiles({ workspaceId, query, limit: 50 }).then(
      (r) => {
        if (n !== seq.current) return;
        setFound(r.files);
        setTotal(r.total);
        setTruncated(r.truncated);
        setError("");
        setSel(0);
      },
      (err) => n === seq.current && setError(friendlyError(err)),
    );
  }, [workspaceId, query]);

  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[data-i="${sel}"]`)?.scrollIntoView({ block: "nearest" });
  }, [sel]);

  const open = (f?: FoundFile) => {
    if (!f) return;
    onClose();
    onOpen(f.path, line);
  };

  const rel = (p: string) => (root === "/" ? p.slice(1) : p.slice(root.length + 1));
  return (
    <div className="code-palette__scrim" onMouseDown={onClose}>
      <div className="code-palette" role="dialog" aria-label="Quick Open" onMouseDown={(e) => e.stopPropagation()}>
        <input
          className="code-palette__input"
          aria-label="File to open"
          placeholder="Search files by name (append :line to go to a line)"
          autoFocus
          spellCheck={false}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              const d = e.key === "ArrowDown" ? 1 : -1;
              setSel((s) => (found.length ? (s + d + found.length) % found.length : 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              open(found[sel]);
            } else if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            }
          }}
        />
        <div className="code-palette__list" role="listbox" aria-label="Files" ref={list}>
          {found.map((f, i) => {
            const r = rel(f.path);
            const name = baseOf(r);
            const dirLen = r.length - name.length;
            const hit = new Set(f.positions);
            return (
              <div
                key={f.path}
                data-i={i}
                role="option"
                aria-selected={i === sel}
                className={`code-palette__item${i === sel ? " code-palette__item--on" : ""}`}
                onMouseMove={() => setSel(i)}
                onClick={() => open(f)}
              >
                <span className="code-palette__name">{marked(name, dirLen, hit)}</span>
                <span className="code-palette__dir">{marked(r.slice(0, Math.max(0, dirLen - 1)), 0, hit)}</span>
              </div>
            );
          })}
          {error && <div className="code-palette__note">{error}</div>}
          {!error && total !== null && found.length === 0 && <div className="code-palette__note">{query ? "No matching files." : "The folder has no files."}</div>}
          {!error && total === null && <div className="code-palette__note">Listing files…</div>}
        </div>
        {truncated && <div className="code-palette__foot">Only the first 100 000 files are listed.</div>}
      </div>
    </div>
  );
}

// marked shows text with the characters at offset+i in hit in bold.
function marked(text: string, offset: number, hit: Set<number>) {
  const out: React.ReactNode[] = [];
  let run = "";
  let on = false;
  const flush = (k: number) => {
    if (run) out.push(on ? <b key={k}>{run}</b> : run);
    run = "";
  };
  for (let i = 0; i < text.length; i++) {
    const h = hit.has(offset + i);
    if (h !== on) {
      flush(i);
      on = h;
    }
    run += text[i];
  }
  flush(text.length);
  return out;
}
