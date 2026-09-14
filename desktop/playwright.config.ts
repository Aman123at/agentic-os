// Playwright config for the Desktop's e2e + performance gate (PLAN.md §16, §17,
// M3.5). The whole suite runs against a real aosd (the `ui` image) driven by the
// fake model provider, brought up and torn down by global-setup/global-teardown
// using the same Docker Compose pattern as tools/e2e. Run it via the single
// entry point: `go run ./tools/ci playwright`.
import { defineConfig, devices } from "@playwright/test";

import { BASE_URL, STATE_FILE } from "./e2e/harness";

export default defineConfig({
  testDir: "./e2e",
  // The suite shares one aosd, so state (Tasks, Approvals, Sessions) is global:
  // run serially to keep specs from stepping on each other.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 20_000 },
  reporter: [["list"]],
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  use: {
    baseURL: BASE_URL,
    storageState: STATE_FILE,
    actionTimeout: 15_000,
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
