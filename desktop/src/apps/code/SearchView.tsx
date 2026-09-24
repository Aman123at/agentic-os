// Find in Files (PLAN.md M8.7): ⌘⇧F, lines under the Workspace that match a
// string or a regular expression, grouped by file. aosd searches as the
// Realm's user with ripgrep, or its own search on a Machine without it
// (CodeService.SearchText), honouring .gitignore; results stream in as they
// are found, and a click opens the file at the match. The list is virtualised
// (§4.3 rule 5): a search can return 2000 lines.
import { Code as RpcCode, ConnectError } from "@connectrpc/connect";
import { useEffect, useRef, useState, type CSSProperties } from "react";

import { code } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { SearchTextSummary, TextFileMatches, TextMatch } from "../../gen/aos/v1/services_pb";
import { VirtualList } from "../../ui/VirtualList";
import { baseOf, relative } from "./paths";

const ROW = 22;
// How long typing has to pause before the search runs.
const DEBOUNCE_MS = 300;

type Row = { kind: "file"; f: TextFileMatches } | { kind: "match"; f: TextFileMatches; m: TextMatch; i: number };

// Request asks the view to take focus, optionally with text to search for;
// n changes each time.
export interface SearchRequest {
  n: number;
  text?: string;
}

export function SearchView({
  workspaceId,
  root,
  request,
  onReveal,
}: {
  workspaceId: string;
  root: string;
  request: SearchRequest;
  onReveal: (path: string, line: number, find: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [caseSensitive, setCase] = useState(false);
  const [regex, setRegex] = useState(false);
  const [more, setMore] = useState(false);
  const [include, setInclude] = useState("");
  const [exclude, setExclude] = useState("");
  const [results, setResults] = useState<TextFileMatches[]>([]);
  const [summary, setSummary] = useState<SearchTextSummary | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const input = useRef<HTMLInputElement>(null);
  const abort = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!request.n) return;
    if (request.text) setQuery(request.text);
    requestAnimationFrame(() => {
      input.current?.focus();
      input.current?.select();
    });
  }, [request]);

  const run = async () => {
    abort.current?.abort();
    setResults([]);
    setSummary(null);
    setError("");
    setCollapsed(new Set());
    if (!query) {
      setRunning(false);
      return;
    }
    const ctrl = new AbortController();
    abort.current = ctrl;
    setRunning(true);
    const globs = (s: string) => s.split(",").map((g) => g.trim()).filter(Boolean);
    try {
      const req = { workspaceId, query, regex, caseSensitive, include: globs(include), exclude: globs(exclude) };
      for await (const msg of code.searchText(req, { signal: ctrl.signal })) {
        if (msg.files.length) setResults((r) => [...r, ...msg.files]);
        if (msg.summary) setSummary(msg.summary);
      }
    } catch (err) {
      if (ctrl.signal.aborted) return;
      const e = ConnectError.from(err);
      setError(e.code === RpcCode.InvalidArgument ? e.rawMessage : friendlyError(err));
    } finally {
      if (abort.current === ctrl) setRunning(false);
    }
  };
  const runRef = useRef(run);
  runRef.current = run;

  // The search runs as typing pauses, and again when an option changes.
  useEffect(() => {
    const t = setTimeout(() => void runRef.current(), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [query, regex, caseSensitive, include, exclude, workspaceId]);
  useEffect(() => () => abort.current?.abort(), []);

  const rows: Row[] = [];
  for (const f of results) {
    rows.push({ kind: "file", f });
    if (!collapsed.has(f.path)) f.matches.forEach((m, i) => rows.push({ kind: "match", f, m, i }));
  }

  const toggle = (p: string) =>
    setCollapsed((c) => {
      const next = new Set(c);
      if (!next.delete(p)) next.add(p);
      return next;
    });

  const count = summary?.matches ?? results.reduce((n, f) => n + f.matches.length, 0);
  return (
    <div className="code-search" aria-label="Search">
      <div className="code-explorer__head">
        <span className="code-explorer__title">Search</span>
        <button className="code__icon-btn" title="Collapse All" aria-label="Collapse All Results" onClick={() => setCollapsed(new Set(results.map((f) => f.path)))}>
          ⊟
        </button>
      </div>
      <div className="code-search__form">
        <div className="code-search__field">
          <input
            ref={input}
            className="code-search__input"
            aria-label="Search for"
            placeholder="Search"
            spellCheck={false}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void run();
              }
            }}
          />
          <button className={`code-search__opt${caseSensitive ? " code-search__opt--on" : ""}`} aria-pressed={caseSensitive} title="Match Case" onClick={() => setCase(!caseSensitive)}>
            Aa
          </button>
          <button className={`code-search__opt${regex ? " code-search__opt--on" : ""}`} aria-pressed={regex} title="Use Regular Expression" onClick={() => setRegex(!regex)}>
            .*
          </button>
        </div>
        <button className="code-search__more" aria-expanded={more} onClick={() => setMore(!more)}>
          {more ? "▾" : "▸"} files to include and exclude
        </button>
        {more && (
          <>
            <input
              className="code-search__input"
              aria-label="Files to include"
              placeholder="e.g. *.go, src/**"
              spellCheck={false}
              value={include}
              onChange={(e) => setInclude(e.target.value)}
            />
            <input
              className="code-search__input"
              aria-label="Files to exclude"
              placeholder="e.g. *.test.ts, vendor"
              spellCheck={false}
              value={exclude}
              onChange={(e) => setExclude(e.target.value)}
            />
          </>
        )}
      </div>
      <div className="code-search__summary" role="status" aria-label="Search results summary">
        {error ? (
          <span className="code-search__error">{error}</span>
        ) : running && !summary ? (
          `Searching… ${count ? `${count} results so far` : ""}`
        ) : summary ? (
          <>
            {summary.matches === 0
              ? "No results."
              : `${summary.matches} ${summary.matches === 1 ? "result" : "results"} in ${summary.files} ${summary.files === 1 ? "file" : "files"}`}
            {summary.truncated && " — the first 2000; narrow the search to see the rest."}
            {summary.engine === "built-in" && <div className="code-search__engine">ripgrep isn’t installed, so AOS searched on its own; it is slower on big folders.</div>}
          </>
        ) : null}
      </div>
      <VirtualList className="code-search__results" items={rows} rowHeight={ROW} renderRow={(row, style) => renderRow(row, style)} />
    </div>
  );

  function renderRow(row: Row, style: CSSProperties) {
    if (row.kind === "file") {
      const rel = relative(row.f.path, root);
      const dir = rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : "";
      const shut = collapsed.has(row.f.path);
      return (
        <div key={`f:${row.f.path}`} className="code-search__file" style={style} title={rel} onClick={() => toggle(row.f.path)} role="treeitem" aria-expanded={!shut}>
          <span className="code-explorer__twisty">{shut ? "▸" : "▾"}</span>
          <span className="code-search__fname">{baseOf(row.f.path)}</span>
          <span className="code-search__fdir">{dir}</span>
          <span className="code-search__count">{row.f.matches.length}</span>
        </div>
      );
    }
    const { m, f } = row;
    const first = m.ranges[0];
    return (
      <div
        key={`m:${f.path}:${m.line}:${row.i}`}
        className="code-search__match"
        style={style}
        role="treeitem"
        aria-label={`${baseOf(f.path)} line ${m.line}`}
        title={`Line ${m.line}`}
        onClick={() => onReveal(f.path, m.line, first ? m.text.slice(first.start, first.end) : "")}
      >
        {highlighted(m)}
      </div>
    );
  }
}

function highlighted(m: TextMatch) {
  const out: React.ReactNode[] = [];
  let at = 0;
  m.ranges.forEach((r, i) => {
    if (r.start > at) out.push(m.text.slice(at, r.start));
    out.push(<mark key={i}>{m.text.slice(r.start, r.end)}</mark>);
    at = r.end;
  });
  out.push(m.text.slice(at));
  return out;
}
