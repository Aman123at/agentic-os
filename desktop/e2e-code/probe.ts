// Loaded into the page by languages.spec.ts through Vite, so it runs on the
// same CodeMirror modules the Desktop does: highlights a text with one of
// languages.ts's grammars and returns each highlighted run with its classes.
import { ensureSyntaxTree } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { classHighlighter, highlightTree } from "@lezer/highlight";

import type { LangId } from "../src/editor/detect";
import { loadLanguage } from "../src/editor/languages";

export async function tokens(id: LangId, text: string): Promise<[string, string][]> {
  const state = EditorState.create({ doc: text, extensions: [await loadLanguage(id)] });
  const tree = ensureSyntaxTree(state, text.length, 5000);
  if (!tree) throw new Error(`${id}: not parsed`);
  const out: [string, string][] = [];
  highlightTree(tree, classHighlighter, (from, to, cls) => out.push([text.slice(from, to), cls]));
  return out;
}
