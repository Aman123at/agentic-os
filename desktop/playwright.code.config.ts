// Playwright config for Code's specs (PLAN.md M8). Like e2e-auth, no Docker and
// no aosd: a Vite dev server serves the Desktop and e2e-code/fake.ts stands in
// for aosd inside the page, with a file tree, the event stream and CodeService.
// Run it via `npm run e2e:code`, or `go run ./tools/ci code-ui`.
import { defineConfig, devices } from "@playwright/test";

const PORT = 5198;

export default defineConfig({
  testDir: "./e2e-code",
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
