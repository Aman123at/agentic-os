// Global setup for the Desktop *live* suite (PLAN.md §16, §17, M4.7): bring the
// `ui` Machine up against a real provider (the OPENAI_* settings from .env — no
// fake model, no cassettes), wait for aosd, sign a browser in once and save the
// session. The counterpart to e2e/global-setup.ts; the difference is the provider
// and that this run spends your key, so it refuses to start without a real one.
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";

import { chromium } from "@playwright/test";

import {
  LIVE_BASE_URL,
  LIVE_PORT,
  LIVE_STATE_FILE,
  REPO_ROOT,
  type Compose,
  docker,
  ensureLiveArtifacts,
  mintCode,
  readDotenv,
  requireRealKey,
  resetSpend,
  saveLiveCompose,
  signIn,
} from "./live-harness";

export default async function globalSetup(): Promise<void> {
  ensureLiveArtifacts();
  resetSpend();

  // The provider settings come from the repo's .env, which you fill in yourself.
  // Refuse to spend anything until there is a real key there.
  const dotenv = readDotenv();
  requireRealKey(dotenv);

  // A scratch folder in the system temp area, never the repository: Docker
  // Desktop on macOS can't mount from ~/Desktop without extra access.
  const dir = mkdtempSync(path.join(os.tmpdir(), "aos-live-"));
  const shared = path.join(dir, "shared");
  mkdirSync(shared, { recursive: true });

  // The live Machine's environment: the real provider settings, on our own Host
  // port and Shared Folder. Autonomy is `auto` so the suite's single benign Task
  // (writing a short note) runs unattended — approval semantics are covered by
  // the fake suite (e2e/approval.spec.ts, e2e/agent.spec.ts), not here.
  const env: Record<string, string> = {
    AOS_MODE: "ui",
    AOS_PORT: LIVE_PORT,
    AOS_BIND: "127.0.0.1",
    AOS_SHARED_DIR: shared,
    AOS_AUTONOMY: "auto",
    OPENAI_API_KEY: dotenv.OPENAI_API_KEY,
  };
  for (const k of ["OPENAI_MODEL", "OPENAI_REASONING_EFFORT", "OPENAI_BASE_URL"]) {
    if (dotenv[k]) env[k] = dotenv[k];
  }

  const envFile = path.join(dir, "live.env");
  writeFileSync(
    envFile,
    Object.entries(env)
      .map(([k, v]) => `${k}=${v}`)
      .join("\n") + "\n",
  );

  const compose: Compose = {
    project: "aos-live",
    envFile,
    files: [path.join(REPO_ROOT, "compose.yaml")], // no fake-provider override
    dir,
    env,
  };
  saveLiveCompose(compose);

  // Build (reusing the image cache), then start from nothing, as a new user does.
  console.log(`[live] building the ui Machine (real provider${env.OPENAI_MODEL ? `, model ${env.OPENAI_MODEL}` : ""})…`);
  docker(compose, ["build"]);
  docker(compose, ["down", "-v", "--remove-orphans"]);

  const browser = await chromium.launch();
  try {
    const start = Date.now();
    docker(compose, ["up", "-d"]);
    await waitForAosd(compose, start + 90_000);
    const apiMs = Date.now() - start;
    const context = await browser.newContext({ baseURL: LIVE_BASE_URL });
    const page = await context.newPage();
    await signIn(page, mintCode(compose));
    const paintMs = Date.now() - start;
    // Startup does not call the model, so we report the §16 numbers for context
    // but do not gate on them here — the fake suite is the startup gate.
    console.log(`[live] compose up: aosd answers in ${seconds(apiMs)}, the Desktop paints in ${seconds(paintMs)}`);
    await context.storageState({ path: LIVE_STATE_FILE });
    await context.close();
  } finally {
    await browser.close();
  }
  console.log("[live] signed in; session saved. The suite will now spend the key.");
}

// waitForAosd polls aosd's health check on the Host port until it answers.
async function waitForAosd(c: Compose, deadline: number): Promise<void> {
  for (;;) {
    try {
      if ((await fetch(`${LIVE_BASE_URL}/healthz`)).ok) return;
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) {
      const logs = docker(c, ["logs", "--tail", "40", "aos"]);
      throw new Error(`aosd did not come up in time:\n${logs}`);
    }
    await new Promise((r) => setTimeout(r, 50));
  }
}

function seconds(ms: number): string {
  return `${(ms / 1000).toFixed(1)} s`;
}
