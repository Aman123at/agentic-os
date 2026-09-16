// Playwright config for the Desktop's *live* suite (PLAN.md §16, §17, M4.7): the
// real-model counterpart to playwright.config.ts. It drives a real aosd (the `ui`
// image) against the provider configured in .env, so it spends your key and is
// never part of `go run ./tools/ci`'s default sweep — run it deliberately with
// `go run ./tools/ci live` (or `npm run e2e:live`).
//
// Two things differ from the fake config: it runs headed on a real GPU (so the
// drag spec can assert dropped frames, which a headless software compositor can't
// represent), and its timeouts allow for real model latency.
import { defineConfig, devices } from "@playwright/test";

import { LIVE_BASE_URL, LIVE_STATE_FILE } from "./e2e-live/live-harness";

export default defineConfig({
  testDir: "./e2e-live",
  // One shared aosd, so state is global: run serially, like the fake suite.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 180_000, // room for a real model to finish a Task
  expect: { timeout: 20_000 },
  reporter: [["list"]],
  globalSetup: "./e2e-live/global-setup.ts",
  globalTeardown: "./e2e-live/global-teardown.ts",
  use: {
    baseURL: LIVE_BASE_URL,
    storageState: LIVE_STATE_FILE,
    actionTimeout: 15_000,
    trace: "retain-on-failure",
    // Headed on a real GPU: the drag spec measures actual presented frames.
    headless: false,
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
