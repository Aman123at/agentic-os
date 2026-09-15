// Global setup for the Desktop e2e suite (PLAN.md §17, M3.5): start the `ui`
// Machine under Docker Compose with the fake model provider (no OpenAI, no
// spend), wait for aosd, then sign a browser in once and save the session so the
// specs start authenticated. Mirrors tools/e2e's compose pattern.
import { cpSync, mkdirSync, mkdtempSync, readdirSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";

import { chromium, type FullConfig } from "@playwright/test";

import {
  BASE_URL,
  COMPOSE_UP_TARGET_MS,
  IDLE_MS,
  PORT,
  REPO_ROOT,
  STATE_FILE,
  checkIdleMemory,
  docker,
  ensureArtifacts,
  mintCode,
  saveCompose,
  signIn,
  type Compose,
} from "./harness";

export default async function globalSetup(_config: FullConfig): Promise<void> {
  ensureArtifacts();

  // A scratch folder in the system temp area, never the repository: Docker
  // Desktop on macOS can't mount from ~/Desktop without extra access.
  const dir = mkdtempSync(path.join(os.tmpdir(), "aos-pw-"));
  const shared = path.join(dir, "shared");
  const cassettes = path.join(dir, "cassettes");
  mkdirSync(shared, { recursive: true });
  mkdirSync(cassettes, { recursive: true });

  // The recorded conversations stand in for OpenAI. The same folder tools/e2e
  // uses, so cassettes live in one place.
  const src = path.join(REPO_ROOT, "tools", "e2e", "testdata", "cassettes");
  for (const f of readdirSync(src)) {
    if (f.endsWith(".json")) cpSync(path.join(src, f), path.join(cassettes, f));
  }

  const envFile = path.join(dir, "pw.env");
  const env: Record<string, string> = {
    AOS_MODE: "ui",
    AOS_PORT: PORT,
    AOS_BIND: "127.0.0.1",
    AOS_SHARED_DIR: shared,
    AOS_AUTONOMY: "confirm-risky",
    OPENAI_API_KEY: "sk-pw-dummy-not-a-real-key",
  };
  writeFileSync(
    envFile,
    Object.entries(env)
      .map(([k, v]) => `${k}=${v}`)
      .join("\n") + "\n",
  );

  // A compose override adds the fake provider and mounts the cassettes read-only.
  const override = path.join(dir, "compose.pw.yaml");
  writeFileSync(
    override,
    `services:\n  aos:\n    environment:\n      AOS_FAKE_MODEL: /pw/cassettes\n    volumes:\n      - ${cassettes}:/pw/cassettes:ro\n`,
  );

  const compose: Compose = {
    project: "aos-pw",
    envFile,
    files: [path.join(REPO_ROOT, "compose.yaml"), override],
    dir,
    env,
  };
  saveCompose(compose);

  // Build first, so the §16 timer below covers `docker compose up` alone, and
  // start from nothing, as a new user does (a Machine a failed run left behind
  // would make the start look instant).
  console.log("[pw] building the ui Machine (fake provider)…");
  docker(compose, ["build"]);
  docker(compose, ["down", "-v", "--remove-orphans"]);

  // PLAN.md §16: `docker compose up` to usable is under 5 s, measured from the
  // command to aosd answering and on to the Desktop's first paint. The browser
  // is started before the clock, as a user's already is.
  const browser = await chromium.launch();
  let paintMs: number;
  try {
    const start = Date.now();
    docker(compose, ["up", "-d"]);
    await waitForAosd(compose, start + 90_000);
    const apiMs = Date.now() - start;
    const context = await browser.newContext({ baseURL: BASE_URL });
    const page = await context.newPage();
    await signIn(page, mintCode(compose));
    paintMs = Date.now() - start;
    console.log(
      `[pw] §16 compose up: aosd answers in ${seconds(apiMs)}, the Desktop paints in ${seconds(paintMs)} (target < ${seconds(COMPOSE_UP_TARGET_MS)})`,
    );
    await context.storageState({ path: STATE_FILE });
    await context.close();
  } finally {
    await browser.close();
  }
  console.log("[pw] signed in; session saved.");
  if (paintMs >= COMPOSE_UP_TARGET_MS) {
    throw new Error(
      `docker compose up took ${seconds(paintMs)} to the Desktop's first paint; the target is under ${seconds(COMPOSE_UP_TARGET_MS)} (PLAN.md §16)`,
    );
  }

  // §16: aosd's idle memory, checked again after the suite (global-teardown.ts).
  await sleep(IDLE_MS);
  checkIdleMemory(compose, "after startup");
}

// waitForAosd polls aosd's health check on the Host port until it answers.
async function waitForAosd(c: Compose, deadline: number): Promise<void> {
  for (;;) {
    try {
      if ((await fetch(`${BASE_URL}/healthz`)).ok) return;
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) {
      const logs = tail(docker(c, ["logs", "--tail", "40", "aos"]));
      throw new Error(`aosd did not come up in time:\n${logs}`);
    }
    await sleep(50);
  }
}

function seconds(ms: number): string {
  return `${(ms / 1000).toFixed(1)} s`;
}

function tail(s: string): string {
  const lines = s.trimEnd().split("\n");
  return lines.slice(Math.max(0, lines.length - 40)).join("\n");
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
