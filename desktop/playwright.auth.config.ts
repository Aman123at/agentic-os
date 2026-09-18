// Playwright config for the authentication screens (PLAN.md §18 M6.5). Unlike the
// main suite, this one needs no Docker and no aosd: it serves the Desktop from a
// Vite dev server and fakes aosd's auth RPCs with route interception (see
// e2e-auth/fake.ts), so the three boot phases, the expiry modal, the boot-time
// refresh and logout can be driven on any machine. Run it via `npm run e2e:auth`.
import { defineConfig, devices } from "@playwright/test";

const PORT = 5199;

export default defineConfig({
  testDir: "./e2e-auth",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 8_000 },
  reporter: [["list"]],
  webServer: {
    command: `npx vite --port ${PORT} --strictPort`,
    url: `http://localhost:${PORT}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
  use: {
    baseURL: `http://localhost:${PORT}`,
    actionTimeout: 8_000,
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
