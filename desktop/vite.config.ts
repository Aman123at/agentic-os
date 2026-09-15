import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Desktop is served by aosd from the ui image's embedded assets, at the
// site root. In `npm run dev`, proxy the API to a locally running aosd so the
// app can talk to real services (PLAN.md §4.3).
const target = process.env.AOS_DEV_TARGET ?? "http://localhost:7700";
const proxy = { target, changeOrigin: false, ws: true };

export default defineConfig({
  plugins: [react()],
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
