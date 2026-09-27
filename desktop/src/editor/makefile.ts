// Makefile highlighting (PLAN.md M8.9): CodeMirror's legacy modes have none.
// Comments, directives, variable definitions and targets, $(references), and
// recipe lines (a tab first), where only references and strings stand out.
import type { StreamParser } from "@codemirror/language";

interface State {
  recipe: boolean; // this line is a recipe, or continues one
  head: boolean; // nothing but spaces read on this line yet
  define: boolean; // inside define … endef
  cont: boolean; // the line before ended with a backslash
}

const DIRECTIVE = /^-?(?:include|sinclude|ifeq|ifneq|ifdef|ifndef|else|endif|define|endef|export|unexport|override|private|undefine|vpath)\b/;
const ASSIGN = /^[^\s:#=$()]+(?=\s*(?:::?=|:::=|\?=|\+=|!=|=))/;
const TARGET = /^[^\s:#=$][^:#=$]*?(?=\s*::?(?!=))/;
const AUTOMATIC = /[@<^+?*%|$]/;

// reference reads $(…), ${…}, $@ and the like, with nesting; the $ is read.
function reference(stream: Parameters<StreamParser<State>["token"]>[0]): string {
  const open = stream.peek();
  if (open !== "(" && open !== "{") {
    if (open && (AUTOMATIC.test(open) || /\w/.test(open))) stream.next();
    return "variableName.special";
  }
  const close = open === "(" ? ")" : "}";
  let depth = 0;
  for (let c = stream.next(); c != null; c = stream.next()) {
    if (c === open) depth++;
    else if (c === close && --depth === 0) break;
  }
  return "variableName.special";
}

export const makefile: StreamParser<State> = {
  name: "makefile",
  startState: () => ({ recipe: false, head: true, define: false, cont: false }),
  blankLine: (state) => {
    state.cont = false;
  },
  token(stream, state) {
    if (stream.sol()) {
      if (!state.cont) state.recipe = stream.peek() === "\t";
      state.head = !state.cont;
      state.cont = /\\$/.test(stream.string);
    }
    if (stream.eatSpace()) return null;
    const head = state.head;
    state.head = false;
    const ch = stream.peek();

    if (ch === "#" && !(state.recipe || state.define)) {
      stream.skipToEnd();
      return "comment";
    }
    if (ch === "$") {
      stream.next();
      return reference(stream);
    }
    if (state.define) {
      if (head && stream.match(/^endef\b/)) {
        state.define = false;
        return "keyword";
      }
      stream.eatWhile(/[^$]/);
      return null;
    }
    if (state.recipe) {
      if (ch === '"' || ch === "'") {
        stream.next();
        while (!stream.eol()) {
          const c = stream.next();
          if (c === "\\") stream.next();
          else if (c === ch) break;
        }
        return "string";
      }
      if (ch === "#" && head) {
        stream.skipToEnd();
        return "comment";
      }
      stream.eatWhile(/[^$"'\\]/);
      if (stream.peek() === "\\") stream.next();
      return null;
    }
    if (head) {
      const m = stream.match(DIRECTIVE) as RegExpMatchArray | null;
      if (m) {
        if (m[0] === "define") state.define = true;
        return "keyword";
      }
      if (stream.match(ASSIGN)) return "variableName.definition";
      if (stream.match(TARGET)) return "variableName.function.definition";
    }
    if (stream.match(/^(?:::?=|:::=|\?=|\+=|!=|=|::?|;)/)) return "operator";
    stream.next();
    stream.eatWhile(/[^\s$#=:;]/);
    return null;
  },
  languageData: {
    commentTokens: { line: "#" },
  },
};
