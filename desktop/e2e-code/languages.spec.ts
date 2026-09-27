// Languages (PLAN.md M8.9): which language a file is in, by its name and then
// its #! line; and that each language's grammar loads, as its own chunk, and
// highlights what it should.
import { expect, test } from "@playwright/test";

import { byShebang, detect, firstLine, LANGUAGE_NAMES, languageName, type LangId } from "../src/editor/detect";

const NAMES: [string, LangId | null][] = [
  ["main.go", "go"],
  ["src/index.mjs", "javascript"],
  ["src/Button.jsx", "jsx"],
  ["src/types.d.ts", "typescript"],
  ["src/App.tsx", "tsx"],
  ["tools/gen.py", "python"],
  ["stubs/os.pyi", "python"],
  ["src/lib.rs", "rust"],
  ["main.c", "c"],
  ["util.h", "c"],
  ["engine.cpp", "cpp"],
  ["engine.hpp", "cpp"],
  ["Program.cs", "csharp"],
  ["Main.java", "java"],
  ["build.gradle.kts", "kotlin"],
  ["App.swift", "swift"],
  ["index.php", "php"],
  ["Gemfile", "ruby"],
  ["tasks.rake", "ruby"],
  ["deploy.pl", "perl"],
  ["init.lua", "lua"],
  ["analysis.R", "r"],
  ["schema.sql", "sql"],
  ["index.HTML", "html"],
  ["site.css", "css"],
  ["site.scss", "scss"],
  ["site.sass", "sass"],
  ["site.less", "less"],
  ["package.json", "json"],
  ["docker-compose.yml", "yaml"],
  ["Cargo.toml", "toml"],
  ["Cargo.lock", "toml"],
  ["pom.xml", "xml"],
  ["README.md", "markdown"],
  ["install.sh", "shell"],
  ["/home/aos/.bashrc", "shell"],
  ["setup.ps1", "powershell"],
  ["Dockerfile", "dockerfile"],
  ["Dockerfile.dev", "dockerfile"],
  ["api.dockerfile", "dockerfile"],
  ["Makefile", "makefile"],
  ["GNUmakefile", "makefile"],
  ["rules.mk", "makefile"],
  ["/etc/nginx/nginx.conf", "nginx"],
  ["/etc/nginx/sites-available/default", "nginx"],
  ["/etc/nginx/conf.d/app.conf", "nginx"],
  ["deploy/nginx/README.md", "markdown"],
  ["setup.cfg", "ini"],
  [".env", "ini"],
  [".env.local", "ini"],
  ["/etc/systemd/system/aosd.service", "ini"],
  ["proto/aos/v1/services.proto", "protobuf"],
  ["fix.patch", "diff"],
  ["notes.txt", null],
  ["LICENSE", null],
  [".gitignore", null],
];

const SHEBANGS: [string, LangId | null][] = [
  ["#!/bin/bash", "shell"],
  ["#!/bin/sh\r", "shell"],
  ["#!/usr/bin/env bash", "shell"],
  ["#!/usr/bin/env LANG=C bash", "shell"],
  ["#!/usr/bin/env python3", "python"],
  ["#!/usr/bin/env python3.12", "python"],
  ["#!/usr/bin/python", "python"],
  ["#!/usr/bin/env node", "javascript"],
  ["#!/usr/bin/env -S deno run --allow-net", "typescript"],
  ["#!/usr/bin/env ruby", "ruby"],
  ["#!/usr/bin/perl -w", "perl"],
  ["#!/usr/bin/php", "php"],
  ["#!/usr/bin/env lua5.4", "lua"],
  ["#!/usr/bin/env Rscript", "r"],
  ["#!/usr/bin/env pwsh", "powershell"],
  ["#!/usr/bin/make -f", "makefile"],
  ["#!/usr/bin/env fish", null],
  ["# not a shebang", null],
  ["", null],
];

test("a file's language comes from its name", () => {
  for (const [path, want] of NAMES) expect(detect(path), path).toBe(want);
});

test("a file with no telling name gets its language from its #! line", () => {
  for (const [line, want] of SHEBANGS) {
    expect(byShebang(line.replace(/\r$/, "")), line).toBe(want);
    expect(detect("bin/tool", line), line).toBe(want);
  }
  // The name comes first.
  expect(detect("tool.py", "#!/bin/bash")).toBe("python");
  expect(detect("notes.txt", "#!/bin/bash")).toBe("shell");
  expect(firstLine("#!/bin/sh\necho hi\n")).toBe("#!/bin/sh");
  expect(languageName("bin/tool", "#!/usr/bin/env python3")).toBe("Python");
  expect(languageName("LICENSE")).toBe("Plain Text");
});

// A sample of each language, and a run in it that must be highlighted so.
const SAMPLES: Record<LangId, [string, [string, string][]]> = {
  javascript: ["// note\nconst x = 'a';", [["const", "tok-keyword"], ["'a'", "tok-string"]]],
  jsx: ['const a = <div className="x" />;', [["div", "tok-typeName"]]],
  typescript: ["interface A { b: string } // note", [["interface", "tok-keyword"], ["string", "tok-typeName"]]],
  tsx: ["type P = {}; const a = <App p={1} />;", [["type", "tok-keyword"], ["App", "tok-typeName"]]],
  python: ["# note\ndef f(): return 'a'", [["def", "tok-keyword"], ["# note", "tok-comment"]]],
  go: ["// note\nfunc main() {}", [["func", "tok-keyword"]]],
  rust: ['// note\nfn main() { let s = "a"; }', [["fn", "tok-keyword"], ["let", "tok-keyword"]]],
  c: ["/* note */\nint main(void) { return 0; }", [["return", "tok-keyword"], ["/* note */", "tok-comment"]]],
  cpp: ["// note\nclass A { public: int x; };", [["class", "tok-keyword"]]],
  csharp: ["// note\npublic class A { }", [["class", "tok-keyword"]]],
  java: ["// note\npublic class A { }", [["class", "tok-keyword"]]],
  kotlin: ['// note\nfun main() { val s = "a" }', [["fun", "tok-keyword"], ["val", "tok-keyword"]]],
  swift: ['// note\nfunc main() { let s = "a" }', [["func", "tok-keyword"]]],
  php: ["<?php\n// note\nfunction f() { return 'a'; }", [["function", "tok-keyword"], ["<?php", "tok-meta"]]],
  ruby: ["# note\ndef f; 'a'; end", [["def", "tok-keyword"], ["end", "tok-keyword"]]],
  perl: ["# note\nsub f { my $x = 'a'; }", [["sub", "tok-keyword"]]],
  lua: ["-- note\nlocal function f() return 'a' end", [["local", "tok-keyword"], ["-- note", "tok-comment"]]],
  r: ["# note\nf <- function(x) 'a'", [["function", "tok-keyword"]]],
  sql: ["-- note\nSELECT a FROM t WHERE b = 'a';", [["SELECT", "tok-keyword"], ["'a'", "tok-string"]]],
  html: ['<!-- note -->\n<div class="x"><script>const a = 1;</script></div>', [["div", "tok-typeName"], ["const", "tok-keyword"]]],
  css: ["/* note */\n.a { color: red; }", [["color", "tok-propertyName"]]],
  scss: ["// note\n$c: red;\n.a { color: $c; }", [["$c", "tok-variableName2"], ["// note", "tok-comment"]]],
  sass: ["// note\n$c: red\n.a\n  color: $c", [["$c", "tok-variableName2"]]],
  less: ["// note\n@c: red;\n.a { color: @c; }", [["@c", "tok-variableName2"]]],
  json: ['{"a": "b", "t": true}', [['"a"', "tok-propertyName"], ["true", "tok-bool"]]],
  yaml: ["# note\na: 'b'", [["'b'", "tok-string"], ["# note", "tok-comment"]]],
  toml: ["# note\n[a]\nb = 'c'", [["[a]", "tok-atom"], ["'c'", "tok-string"]]],
  xml: ['<!-- note -->\n<a x="1">t</a>', [["x", "tok-propertyName"], ["<!-- note -->", "tok-comment"]]],
  markdown: ["# Title\n\nSome *em*.", [[" Title", "tok-heading"], ["em", "tok-emphasis"]]],
  shell: ["# note\nif [ -n \"$X\" ]; then echo 'a'; fi", [["then", "tok-keyword"], ["'a'", "tok-string"]]],
  powershell: ["# note\nfunction F { $x = 'a' }", [["function", "tok-keyword"]]],
  dockerfile: ["# note\nFROM debian\nRUN echo 'a'", [["FROM", "tok-keyword"], ["RUN", "tok-keyword"]]],
  makefile: [
    '# note\nCC := gcc\nall: main.o\n\t$(CC) -o app $^ "x" # to the shell\ninclude other.mk\ndefine BODY\n$(X) # not a comment\nendef\n',
    [
      ["# note", "tok-comment"],
      ["CC", "tok-definition"],
      [":=", "tok-operator"],
      ["all", "tok-definition"],
      ["$(CC)", "tok-variableName2"],
      ["$^", "tok-variableName2"],
      ['"x"', "tok-string"],
      ["include", "tok-keyword"],
      ["define", "tok-keyword"],
      ["$(X)", "tok-variableName2"],
      ["endef", "tok-keyword"],
    ],
  ],
  nginx: ["# note\nserver { listen 80; }", [["listen", "tok-keyword"]]],
  ini: ["; note\n[a]\nb = c", [["[a]", "tok-heading"], ["; note", "tok-comment"]]],
  protobuf: ["// note\nmessage A { string b = 1; }", [["message", "tok-keyword"]]],
  diff: ["--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y", [["-x", "tok-deleted"], ["+y", "tok-inserted"]]],
};

test("every language's grammar loads and highlights its sample", async ({ page }) => {
  expect(Object.keys(SAMPLES).sort()).toEqual(Object.keys(LANGUAGE_NAMES).sort());
  await page.goto("/");
  // probe.ts runs in the page, on the Desktop's own CodeMirror modules.
  const got = await page.evaluate(async (samples) => {
    const url = "/e2e-code/probe.ts";
    const probe = (await import(/* @vite-ignore */ url)) as typeof import("./probe");
    const out: Record<string, [string, string][]> = {};
    for (const [id, [text]] of Object.entries(samples)) out[id] = await probe.tokens(id as LangId, text);
    return out;
  }, SAMPLES);
  for (const [id, [, want]] of Object.entries(SAMPLES)) {
    for (const [text, cls] of want) {
      const runs = got[id].filter(([t]) => t === text).map(([, c]) => c);
      expect(runs.some((c) => c.split(" ").includes(cls)), `${id}: ${text} as ${cls}, got ${JSON.stringify(got[id])}`).toBe(true);
    }
  }
  // The Makefile's recipe comment and define body are not Make comments.
  expect(got.makefile.some(([t, c]) => t.includes("not a comment") && c.includes("tok-comment"))).toBe(false);
});
