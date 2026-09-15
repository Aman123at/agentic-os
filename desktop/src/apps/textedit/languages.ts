// TextEdit's syntax highlighting, chosen by the file's name. Each language is
// its own chunk, fetched when a file of that kind opens.
import { StreamLanguage, type StreamParser } from "@codemirror/language";
import type { Extension } from "@codemirror/state";

import { extOf } from "../filetypes";

async function legacy(load: () => Promise<StreamParser<unknown>>): Promise<Extension> {
  return StreamLanguage.define(await load());
}

export async function languageFor(name: string): Promise<Extension | null> {
  const base = name.slice(name.lastIndexOf("/") + 1).toLowerCase();
  if (base === "dockerfile" || base.startsWith("dockerfile.")) return legacy(async () => (await import("@codemirror/legacy-modes/mode/dockerfile")).dockerFile);
  switch (extOf(base)) {
    case "js":
    case "mjs":
    case "cjs":
    case "jsx":
      return (await import("@codemirror/lang-javascript")).javascript({ jsx: true });
    case "ts":
    case "mts":
    case "cts":
      return (await import("@codemirror/lang-javascript")).javascript({ typescript: true });
    case "tsx":
      return (await import("@codemirror/lang-javascript")).javascript({ jsx: true, typescript: true });
    case "json":
      return (await import("@codemirror/lang-json")).json();
    case "md":
    case "markdown":
      return (await import("@codemirror/lang-markdown")).markdown();
    case "py":
      return (await import("@codemirror/lang-python")).python();
    case "html":
    case "htm":
      return (await import("@codemirror/lang-html")).html();
    case "css":
      return (await import("@codemirror/lang-css")).css();
    case "go":
      return (await import("@codemirror/lang-go")).go();
    case "yaml":
    case "yml":
      return (await import("@codemirror/lang-yaml")).yaml();
    case "sh":
    case "bash":
    case "zsh":
      return legacy(async () => (await import("@codemirror/legacy-modes/mode/shell")).shell);
    case "toml":
      return legacy(async () => (await import("@codemirror/legacy-modes/mode/toml")).toml);
    case "rs":
      return legacy(async () => (await import("@codemirror/legacy-modes/mode/rust")).rust);
    case "c":
    case "h":
      return legacy(async () => (await import("@codemirror/legacy-modes/mode/clike")).c);
    case "cc":
    case "cpp":
    case "hpp":
      return legacy(async () => (await import("@codemirror/legacy-modes/mode/clike")).cpp);
    case "java":
      return legacy(async () => (await import("@codemirror/legacy-modes/mode/clike")).java);
  }
  return null;
}
