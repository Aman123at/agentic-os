// Code's navigation in the editor (PLAN.md M8.8): the word under the cursor,
// ⌘-click to a definition, and completion from the language's keywords and
// the Workspace's symbols. The symbols come from aosd's ctags index
// (CodeService.WorkspaceSymbols); it is by name, not type.
import { completeFromList, type Completion, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete";
import { EditorState, type Extension } from "@codemirror/state";
import { EditorView } from "@codemirror/view";

import { code } from "../../api/client";
import type { LangId } from "../../editor/detect";
import type { Symbol as CodeSymbol } from "../../gen/aos/v1/services_pb";
import { relative } from "./paths";

// wordAt is the identifier at pos, or null.
export function wordAt(state: EditorState, pos: number): { name: string; from: number; to: number } | null {
  const line = state.doc.lineAt(pos);
  const at = pos - line.from;
  const re = /[\p{L}\p{N}_$]+/gu;
  for (let m = re.exec(line.text); m; m = re.exec(line.text)) {
    if (m.index <= at && at <= m.index + m[0].length) {
      if (/^\d/.test(m[0])) return null;
      return { name: m[0], from: line.from + m.index, to: line.from + m.index + m[0].length };
    }
  }
  return null;
}

// modClick goes to the definition of the word ⌘-clicked (Ctrl-click off a Mac).
export function modClick(onWord: (pos: number) => void): Extension {
  return EditorView.domEventHandlers({
    mousedown(e, view) {
      if (!(e.metaKey || e.ctrlKey) || e.button !== 0) return false;
      const pos = view.posAtCoords({ x: e.clientX, y: e.clientY });
      if (pos == null || !wordAt(view.state, pos)) return false;
      e.preventDefault();
      view.dispatch({ selection: { anchor: pos } });
      onWord(pos);
      return true;
    },
  });
}

// kindBadge is a symbol's one-letter badge.
export function kindBadge(kind: string): string {
  switch (kind) {
    case "func":
    case "function":
    case "method":
    case "member":
    case "procedure":
    case "subroutine":
      return "ƒ";
    case "class":
    case "struct":
    case "interface":
    case "type":
    case "typedef":
    case "trait":
      return "C";
    case "const":
    case "constant":
    case "enum":
    case "enumerator":
      return "K";
    case "var":
    case "variable":
    case "field":
    case "property":
      return "v";
    case "package":
    case "packageName":
    case "module":
    case "namespace":
      return "▣";
  }
  return "•";
}

function completionType(kind: string): string {
  switch (kindBadge(kind)) {
    case "ƒ":
      return kind === "method" || kind === "member" ? "method" : "function";
    case "C":
      return kind === "interface" ? "interface" : "class";
    case "K":
      return "constant";
    case "v":
      return "variable";
    case "▣":
      return "namespace";
  }
  return "text";
}

// completion offers the Workspace's symbols, and the language's keywords, as
// you type. ws says which Workspace and file; it is read at each request.
export function completion(ws: () => { workspaceId: string; root: string; path: string } | null, lang: LangId | null): Extension {
  const words = keywordsFor(lang);
  const keywords = words.length ? completeFromList(words.map((w): Completion => ({ label: w, type: "keyword" }))) : null;
  const symbols = async (ctx: CompletionContext): Promise<CompletionResult | null> => {
    const word = ctx.matchBefore(/[\p{L}\p{N}_$]+/u);
    if (!word || (word.from === word.to && !ctx.explicit) || word.to - word.from < 2) return null;
    const w = ws();
    if (!w) return null;
    let found: CodeSymbol[];
    try {
      found = (await code.workspaceSymbols({ workspaceId: w.workspaceId, query: word.text, limit: 50, path: w.path })).symbols;
    } catch {
      return null; // no index (no ctags): keywords and the language's own completions still work
    }
    if (ctx.aborted) return null;
    const seen = new Set<string>();
    const options: Completion[] = [];
    for (const s of found) {
      if (seen.has(s.name)) continue;
      seen.add(s.name);
      options.push({ label: s.name, type: completionType(s.kind), detail: `${s.kind} · ${relative(s.path, w.root)}`, boost: -1 });
    }
    return { from: word.from, options, validFor: /^[\p{L}\p{N}_$]*$/u };
  };
  return EditorState.languageData.of(() => [{ autocomplete: symbols }, ...(keywords ? [{ autocomplete: keywords }] : [])]);
}

// keywordsFor are a language's keywords, for completion.
export function keywordsFor(lang: LangId | null): string[] {
  if (!lang) return [];
  for (const [langs, words] of KEYWORDS) if (langs.includes(lang)) return words.split(" ");
  return [];
}

const KEYWORDS: [LangId[], string][] = [
  [
    ["go"],
    "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota append cap close copy delete len make new panic print println recover error string int int64 uint8 byte rune bool float64 any",
  ],
  [
    ["typescript", "tsx", "javascript", "jsx"],
    "async await break case catch class const continue debugger default delete do else export extends finally for from function if import in instanceof interface let new of return static super switch this throw try type typeof var void while yield null undefined true false readonly keyof as satisfies enum implements",
  ],
  [
    ["python"],
    "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield None True False self print len range isinstance",
  ],
  [
    ["rust"],
    "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while Option Some None Result Ok Err Vec String",
  ],
  [["c", "cpp"], "auto break case char const continue default do double else enum extern float for goto if inline int long register return short signed sizeof static struct switch typedef union unsigned void volatile while class namespace template typename public private protected virtual nullptr"],
  [["java", "kotlin"], "abstract boolean break byte case catch char class continue default do double else enum extends final finally float for if implements import instanceof int interface long new package private protected public return short static super switch this throw throws try void while null true false fun val var"],
  [["shell"], "if then else elif fi for while until do done case esac function in return local export echo exit"],
];
