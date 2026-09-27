// The Outline view (PLAN.md M8.8): the symbols of the file in front, as last
// saved, nested by what they are inside; a click goes there. They come from
// aosd's ctags index (CodeService.DocumentSymbols).
import { Code as RpcCode, ConnectError } from "@connectrpc/connect";
import { useEffect, useState } from "react";

import { code } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { Symbol as CodeSymbol } from "../../gen/aos/v1/services_pb";
import { kindBadge } from "./navigate";
import { baseOf } from "./paths";

// A package clause would hold everything; the outline starts below it.
const HIDDEN = new Set(["package", "packageName"]);

// depths nests each symbol under the one its scope names, if the file has it.
function depths(symbols: CodeSymbol[]): number[] {
  const full = new Map<string, number>();
  const out: number[] = [];
  symbols.forEach((s, i) => {
    const parent = s.scope ? (full.get(s.scope) ?? full.get(s.scope.slice(s.scope.lastIndexOf(".") + 1))) : undefined;
    out[i] = parent === undefined ? 0 : Math.min(out[parent] + 1, 4);
    full.set(s.scope ? `${s.scope}.${s.name}` : s.name, i);
    if (!full.has(s.name)) full.set(s.name, i);
  });
  return out;
}

export function Outline({
  workspaceId,
  path,
  version,
  onReveal,
}: {
  workspaceId: string;
  path: string; // the file in front, or ""
  version: string; // changes when the file is saved or reloaded
  onReveal: (path: string, line: number, name: string) => void;
}) {
  const [symbols, setSymbols] = useState<CodeSymbol[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!path) return;
    let live = true;
    setError("");
    code.documentSymbols({ workspaceId, path }).then(
      (r) => live && setSymbols(r.symbols.filter((s) => !HIDDEN.has(s.kind))),
      (err) => {
        if (!live) return;
        const e = ConnectError.from(err);
        setSymbols([]);
        setError(e.code === RpcCode.FailedPrecondition ? e.rawMessage : friendlyError(err));
      },
    );
    return () => {
      live = false;
    };
  }, [workspaceId, path, version]);

  const depth = symbols ? depths(symbols) : [];
  return (
    <div className="code-search" aria-label="Outline">
      <div className="code-explorer__head">
        <span className="code-explorer__title">Outline{path ? ` · ${baseOf(path)}` : ""}</span>
      </div>
      <div className="code-outline" role="tree" aria-label="Symbols">
        {!path && <div className="code-search__summary">Open a file to see its symbols.</div>}
        {path && error && <div className="code-search__summary code-search__error">{error}</div>}
        {path && !error && symbols?.length === 0 && <div className="code-search__summary">No symbols found in this file.</div>}
        {path &&
          symbols?.map((s, i) => (
            <div
              key={`${s.line}:${s.name}:${i}`}
              role="treeitem"
              aria-label={`${s.kind} ${s.name}`}
              className="code-outline__row"
              style={{ paddingLeft: 10 + depth[i] * 14 }}
              title={`${s.kind}${s.signature ? ` ${s.name}${s.signature}` : ""} · line ${s.line}`}
              onClick={() => onReveal(path, s.line, s.name)}
            >
              <span className="code-palette__badge">{kindBadge(s.kind)}</span>
              <span className="code-outline__name">{s.name}</span>
              {s.signature && <span className="code-outline__sig">{s.signature}</span>}
            </div>
          ))}
      </div>
    </div>
  );
}
