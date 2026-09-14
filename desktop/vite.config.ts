import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Desktop is served by aosd from the ui image's embedded assets, at the
// site root. In `npm run dev`, proxy the API to a locally running aosd so the
// app can talk to real services (PLAN.md §4.3).
const target = process.env.AOS_DEV_TARGET ?? "http://localhost:7700";
const proxy = { target, changeOrigin: false, ws: true };

export default defineConfig({
  plugins: [react()],
  build: { target: "es2022", chunkSizeWarningLimit: 200 },
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
