// Placeholder build until the Vite app lands in M3.
import { mkdirSync, writeFileSync } from "node:fs";

mkdirSync("dist", { recursive: true });
writeFileSync(
  "dist/index.html",
  `<!doctype html><meta charset="utf-8"><title>Agentic OS</title>
<p>Agentic OS Desktop placeholder (M0). The real Desktop arrives in M3.</p>
`,
);
