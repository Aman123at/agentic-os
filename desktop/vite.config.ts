import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import type { OutputChunk } from "rollup";

// The Desktop is served by aosd from the ui image's embedded assets, at the
// site root. In `npm run dev`, proxy the API to a locally running aosd so the
// app can talk to real services (PLAN.md §4.3).
const target = process.env.AOS_DEV_TARGET ?? "http://localhost:7700";
const proxy = { target, changeOrigin: false, ws: true };

// What Code fetches only when it is used (PLAN.md M8.14): the Terminal panel
// and xterm.js, compare and Change Review (@codemirror/merge), each language's
// grammar, and pdf.js. The build fails if any of it lands in what opening a
// Code window loads.
const CODE_LOADS_ON_USE =
  /node_modules\/(@xterm\/|@codemirror\/(merge|lang-|legacy-modes)\/|@lezer\/(?!common\/|highlight\/|lr\/)|pdfjs-dist\/)|src\/editor\/changes\.ts|src\/apps\/code\/(TerminalPanel|Compare)\.tsx/;

function codeLoadsOnUse(): Plugin {
  return {
    name: "code-loads-on-use",
    apply: "build",
    generateBundle(_, bundle) {
      const chunks = Object.values(bundle).filter((c): c is OutputChunk => c.type === "chunk");
      const app = chunks.find((c) => c.facadeModuleId?.endsWith("/src/apps/code/Code.tsx"));
      if (!app) return this.error("the Code app's chunk is missing");
      // Everything the chunk imports statically, and what that imports.
      const seen = new Set<string>();
      const todo = [app.fileName];
      for (let f = todo.pop(); f !== undefined; f = todo.pop()) {
        const c = bundle[f];
        if (seen.has(f) || c?.type !== "chunk") continue;
        seen.add(f);
        todo.push(...c.imports);
        const early = Object.keys(c.modules).find((id) => CODE_LOADS_ON_USE.test(id));
        if (early) this.error(`opening Code loads ${early} (in ${f}); it must come with import() when used (PLAN.md M8.14)`);
      }
    },
  };
}

export default defineConfig({
  plugins: [react(), codeLoadsOnUse()],
  build: {
    target: "es2022",
    chunkSizeWarningLimit: 200,
    rollupOptions: {
      output: {
        // TextEdit's language packs are each an `index.js`; name their chunks
        // after the package, so the ui stage's size report can tell them apart.
        chunkFileNames: (chunk) => `assets/${chunk.facadeModuleId?.match(/@codemirror\/(lang-[\w-]+)\//)?.[1] ?? "[name]"}-[hash].js`,
      },
    },
  },
  server: {
    proxy: {
      "/aos.v1.": proxy,
      "/ws": proxy,
      "/files": proxy,
      "/upload": proxy,
      "/healthz": proxy,
    },
  },
});
