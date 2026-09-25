// Which language a file is in (PLAN.md M8.9), for TextEdit's and Code's
// highlighting, Code's status bar and its completion keywords. Chosen by the
// file's name, then by its #! line. Pure, and small: the grammars themselves
// load in languages.ts, each as its own chunk.

export type LangId =
  | "javascript"
  | "jsx"
  | "typescript"
  | "tsx"
  | "python"
  | "go"
  | "rust"
  | "c"
  | "cpp"
  | "csharp"
  | "java"
  | "kotlin"
  | "swift"
  | "php"
  | "ruby"
  | "perl"
  | "lua"
  | "r"
  | "sql"
  | "html"
  | "css"
  | "scss"
  | "sass"
  | "less"
  | "json"
  | "yaml"
  | "toml"
  | "xml"
  | "markdown"
  | "shell"
  | "powershell"
  | "dockerfile"
  | "makefile"
  | "nginx"
  | "ini"
  | "protobuf"
  | "diff";

// What the status bar calls each.
export const LANGUAGE_NAMES: Record<LangId, string> = {
  javascript: "JavaScript",
  jsx: "JavaScript JSX",
  typescript: "TypeScript",
  tsx: "TypeScript JSX",
  python: "Python",
  go: "Go",
  rust: "Rust",
  c: "C",
  cpp: "C++",
  csharp: "C#",
  java: "Java",
  kotlin: "Kotlin",
  swift: "Swift",
  php: "PHP",
  ruby: "Ruby",
  perl: "Perl",
  lua: "Lua",
  r: "R",
  sql: "SQL",
  html: "HTML",
  css: "CSS",
  scss: "SCSS",
  sass: "Sass",
  less: "Less",
  json: "JSON",
  yaml: "YAML",
  toml: "TOML",
  xml: "XML",
  markdown: "Markdown",
  shell: "Shell",
  powershell: "PowerShell",
  dockerfile: "Dockerfile",
  makefile: "Makefile",
  nginx: "Nginx",
  ini: "INI",
  protobuf: "Protocol Buffers",
  diff: "Diff",
};

const BY_EXT: Record<string, LangId> = {};
const BY_NAME: Record<string, LangId> = {};
const exts = (id: LangId, list: string) => list.split(" ").forEach((e) => (BY_EXT[e] = id));
const names = (id: LangId, list: string) => list.split(" ").forEach((n) => (BY_NAME[n] = id));

exts("javascript", "js mjs cjs");
exts("jsx", "jsx");
exts("typescript", "ts mts cts");
exts("tsx", "tsx");
exts("python", "py pyi pyw");
exts("go", "go");
exts("rust", "rs");
exts("c", "c h");
exts("cpp", "cc cpp cxx c++ hpp hh hxx h++ ipp inl ino");
exts("csharp", "cs csx");
exts("java", "java");
exts("kotlin", "kt kts");
exts("swift", "swift");
exts("php", "php phtml php3 php4 php5 phps");
exts("ruby", "rb rake gemspec ru");
exts("perl", "pl pm psgi");
exts("lua", "lua");
exts("r", "r");
exts("sql", "sql");
exts("html", "html htm xhtml");
exts("css", "css");
exts("scss", "scss");
exts("sass", "sass");
exts("less", "less");
exts("json", "json jsonc json5 webmanifest geojson");
exts("yaml", "yaml yml");
exts("toml", "toml");
exts("xml", "xml xsd xsl xslt svg plist csproj fsproj vbproj props targets xaml rss atom wsdl");
exts("markdown", "md markdown mdown mkd");
exts("shell", "sh bash zsh ksh bats");
exts("powershell", "ps1 psm1 psd1");
exts("dockerfile", "dockerfile containerfile");
exts("makefile", "mk mak");
exts("nginx", "nginx");
// systemd's units are INI too, which matters when editing /etc/systemd in Root Mode.
exts("ini", "ini cfg conf properties prefs desktop service socket timer mount");
exts("protobuf", "proto");
exts("diff", "diff patch");

names("dockerfile", "dockerfile containerfile");
names("makefile", "makefile gnumakefile");
names("ruby", "gemfile rakefile guardfile podfile vagrantfile brewfile");
names("shell", ".bashrc .bash_profile .bash_login .bash_logout .profile .zshrc .zprofile .zshenv .zlogin .zlogout .kshrc .envrc");
names("ini", ".env .editorconfig .gitconfig .gitmodules .npmrc .pypirc");
names("toml", "pipfile cargo.lock poetry.lock");
names("nginx", "nginx.conf");

// byName is the language a path's name says, or null.
export function byName(path: string): LangId | null {
  const base = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
  const exact = BY_NAME[base];
  if (exact) return exact;
  // Dockerfile.dev, .env.local; nginx's own folder, where sites have no extension.
  if (base.startsWith("dockerfile.") || base.startsWith("containerfile.")) return "dockerfile";
  if (base.startsWith(".env.")) return "ini";
  const dot = base.lastIndexOf(".");
  const ext = dot > 0 ? base.slice(dot + 1) : "";
  if ((ext === "" || ext === "conf") && /(^|\/)nginx\//.test(path)) return "nginx";
  return BY_EXT[ext] ?? null;
}

const INTERPRETERS: Record<string, LangId> = {};
const interpreters = (id: LangId, list: string) => list.split(" ").forEach((p) => (INTERPRETERS[p] = id));
interpreters("shell", "sh bash zsh ksh mksh dash ash busybox");
interpreters("python", "python pypy");
interpreters("javascript", "node nodejs");
interpreters("typescript", "deno bun ts-node tsx");
interpreters("ruby", "ruby jruby");
interpreters("perl", "perl");
interpreters("php", "php");
interpreters("lua", "lua luajit");
interpreters("r", "rscript");
interpreters("powershell", "pwsh powershell");
interpreters("makefile", "make gmake");
interpreters("swift", "swift");
interpreters("kotlin", "kotlin kotlinc");

// byShebang is the language a #! line names, or null: "#!/bin/bash",
// "#!/usr/bin/env python3", "#!/usr/bin/env -S deno run".
export function byShebang(firstLine: string): LangId | null {
  if (!firstLine.startsWith("#!")) return null;
  const words = firstLine.slice(2).trim().split(/\s+/);
  let prog = baseName(words[0] ?? "");
  if (prog === "env") {
    // env's own options and VAR=value settings come before the program.
    prog = baseName(words.slice(1).find((w) => !w.startsWith("-") && !w.includes("=")) ?? "");
  }
  // python3.12, ruby2.7, php8: the version is not part of the name.
  prog = prog.toLowerCase().replace(/[\d.]+$/, "");
  return INTERPRETERS[prog] ?? null;
}

function baseName(p: string): string {
  return p.slice(p.lastIndexOf("/") + 1);
}

// detect is a file's language: by its name, then by its first line's #!.
export function detect(path: string, firstLine = ""): LangId | null {
  return byName(path) ?? byShebang(firstLine.replace(/\r$/, ""));
}

// firstLine is the start of a text, up to its first line break.
export function firstLine(text: string): string {
  const nl = text.indexOf("\n");
  return nl < 0 ? text.slice(0, 200) : text.slice(0, Math.min(nl, 200));
}

// languageName is what Code's status bar calls a file's language.
export function languageName(path: string, first = ""): string {
  const id = detect(path, first);
  return id ? LANGUAGE_NAMES[id] : "Plain Text";
}
