// Syntax highlighting for TextEdit and Code (PLAN.md M8.9). detect.ts says
// which language a file is in, by name and then #! line; each grammar here is
// its own chunk, fetched when a file of that kind opens. Lezer grammars where
// CodeMirror has one, its legacy modes for the rest.
import { StreamLanguage, type StreamParser } from "@codemirror/language";
import type { Extension } from "@codemirror/state";

import { detect, type LangId } from "./detect";

export { languageName } from "./detect";

async function legacy(load: () => Promise<StreamParser<unknown>>): Promise<Extension> {
  return StreamLanguage.define(await load());
}

const clike = () => import("@codemirror/legacy-modes/mode/clike");

const LOADERS: Record<LangId, () => Promise<Extension>> = {
  javascript: async () => (await import("@codemirror/lang-javascript")).javascript({ jsx: true }),
  jsx: async () => (await import("@codemirror/lang-javascript")).javascript({ jsx: true }),
  typescript: async () => (await import("@codemirror/lang-javascript")).javascript({ typescript: true }),
  tsx: async () => (await import("@codemirror/lang-javascript")).javascript({ jsx: true, typescript: true }),
  python: async () => (await import("@codemirror/lang-python")).python(),
  go: async () => (await import("@codemirror/lang-go")).go(),
  rust: async () => (await import("@codemirror/lang-rust")).rust(),
  c: async () => (await import("@codemirror/lang-cpp")).cpp(),
  cpp: async () => (await import("@codemirror/lang-cpp")).cpp(),
  java: async () => (await import("@codemirror/lang-java")).java(),
  php: async () => (await import("@codemirror/lang-php")).php(),
  sql: async () => (await import("@codemirror/lang-sql")).sql(),
  html: async () => (await import("@codemirror/lang-html")).html(),
  css: async () => (await import("@codemirror/lang-css")).css(),
  scss: async () => (await import("@codemirror/lang-sass")).sass(),
  sass: async () => (await import("@codemirror/lang-sass")).sass({ indented: true }),
  less: async () => (await import("@codemirror/lang-less")).less(),
  json: async () => (await import("@codemirror/lang-json")).json(),
  yaml: async () => (await import("@codemirror/lang-yaml")).yaml(),
  xml: async () => (await import("@codemirror/lang-xml")).xml(),
  markdown: async () => (await import("@codemirror/lang-markdown")).markdown(),

  csharp: () => legacy(async () => (await clike()).csharp),
  kotlin: () => legacy(async () => (await clike()).kotlin),
  swift: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/swift")).swift),
  ruby: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/ruby")).ruby),
  perl: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/perl")).perl),
  lua: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/lua")).lua),
  r: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/r")).r),
  toml: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/toml")).toml),
  shell: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/shell")).shell),
  powershell: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/powershell")).powerShell),
  dockerfile: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/dockerfile")).dockerFile),
  nginx: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/nginx")).nginx),
  ini: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/properties")).properties),
  protobuf: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/protobuf")).protobuf),
  diff: () => legacy(async () => (await import("@codemirror/legacy-modes/mode/diff")).diff),
  makefile: () => legacy(async () => (await import("./makefile")).makefile as StreamParser<unknown>),
};

// loadLanguage fetches one language's highlighting.
export function loadLanguage(id: LangId): Promise<Extension> {
  return LOADERS[id]();
}

// languageFor is the highlighting for a file, given its path (or name) and
// first line, or null for plain text.
export async function languageFor(path: string, firstLine = ""): Promise<Extension | null> {
  const id = detect(path, firstLine);
  return id ? loadLanguage(id) : null;
}
