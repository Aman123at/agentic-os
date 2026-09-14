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
  PORT,
  REPO_ROOT,
  STATE_FILE,
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

  console.log("[pw] building and starting the ui Machine (fake provider)…");
  docker(compose, ["up", "--build", "-d"]);

  // Wait until aosd answers.
  const deadline = Date.now() + 90_000;
  for (;;) {
    try {
      docker(compose, ["exec", "-T", "aos", "aos", "tasks"]);
      break;
    } catch {
      if (Date.now() > deadline) {
        const logs = tail(docker(compose, ["logs", "--tail", "40", "aos"]));
        throw new Error(`aosd did not come up in time:\n${logs}`);
      }
      await sleep(1000);
    }
  }
  console.log("[pw] aosd is up; signing in…");

  const browser = await chromium.launch();
  try {
    const context = await browser.newContext({ baseURL: BASE_URL });
    const page = await context.newPage();
    await signIn(page, mintCode(compose));
    await context.storageState({ path: STATE_FILE });
    await context.close();
  } finally {
    await browser.close();
  }
  console.log("[pw] signed in; session saved.");
}

function tail(s: string): string {
  const lines = s.trimEnd().split("\n");
  return lines.slice(Math.max(0, lines.length - 40)).join("\n");
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
