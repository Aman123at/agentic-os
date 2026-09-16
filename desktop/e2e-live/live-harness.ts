// Shared plumbing for the Desktop *live* suite (PLAN.md §16, §17, M4.7): the
// real-model counterpart to e2e/. Where the fake suite (e2e/) drives a Machine
// with recorded cassettes and never spends, this one brings the `ui` Machine up
// against a real provider — the OPENAI_* settings from the repo's own .env, which
// you fill in yourself (PLAN.md §19). It spends your key, so it is never part of
// `go run ./tools/ci`'s default sweep; run it deliberately with
// `go run ./tools/ci live` (or `npm run e2e:live`).
//
// This module reuses e2e/harness.ts for everything that is provider-agnostic
// (the Docker Compose wrapper, sign-in, opening apps) and adds only what the live
// run needs on top: its own Host port and artifacts, reading the real key out of
// .env (never logging it), and a small spend ledger the specs append to and the
// teardown totals.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

import { REPO_ROOT, type Compose } from "../e2e/harness";

// A Host port of our own, apart from the user's Machine (7700), tools/e2e (7793)
// and the fake Playwright suite (7794). The Host guard accepts 127.0.0.1.
export const LIVE_PORT = "7795";
export const LIVE_BASE_URL = `http://127.0.0.1:${LIVE_PORT}`;

const here = path.dirname(fileURLToPath(import.meta.url));
export const LIVE_ARTIFACTS = path.join(here, ".artifacts");
export const LIVE_STATE_FILE = path.join(LIVE_ARTIFACTS, "state.json");
const LIVE_COMPOSE_FILE = path.join(LIVE_ARTIFACTS, "compose.json");
const SPEND_FILE = path.join(LIVE_ARTIFACTS, "spend.json");

export function ensureLiveArtifacts(): void {
  if (!existsSync(LIVE_ARTIFACTS)) mkdirSync(LIVE_ARTIFACTS, { recursive: true });
}

export function loadLiveCompose(): Compose {
  return JSON.parse(readFileSync(LIVE_COMPOSE_FILE, "utf8")) as Compose;
}

export function saveLiveCompose(c: Compose): void {
  ensureLiveArtifacts();
  writeFileSync(LIVE_COMPOSE_FILE, JSON.stringify(c, null, 2));
}

// readDotenv parses the repo's .env (KEY=VALUE lines, `#` comments, optional
// surrounding quotes). This is the same file Compose reads; the live suite pulls
// the provider settings from it so a run uses exactly your configured model. The
// key's value is returned but never logged by this suite.
export function readDotenv(): Record<string, string> {
  const file = path.join(REPO_ROOT, ".env");
  const out: Record<string, string> = {};
  if (!existsSync(file)) return out;
  for (const raw of readFileSync(file, "utf8").split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq < 0) continue;
    const k = line.slice(0, eq).trim();
    let v = line.slice(eq + 1).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    out[k] = v;
  }
  return out;
}

// A key that is missing, empty, or one of the placeholders the repo ships with
// means the run would either fail or hit no real provider. requireRealKey stops
// early with a message that says exactly what to do, and never prints the key.
const PLACEHOLDER_KEYS = new Set([
  "sk-test-dummy-ci-not-a-real-key",
  "sk-pw-dummy-not-a-real-key",
]);

export function requireRealKey(env: Record<string, string>): void {
  const key = (env.OPENAI_API_KEY ?? "").trim();
  if (!key || PLACEHOLDER_KEYS.has(key)) {
    throw new Error(
      "the live suite needs a real OPENAI_API_KEY in .env (it spends your key).\n" +
        "  Put your key in .env (git-ignored) with a spending cap on the project,\n" +
        "  then run `go run ./tools/ci live` again. To exercise everything without\n" +
        "  spending, use the fake-provider suite instead: `go run ./tools/ci playwright`.",
    );
  }
}

// The spend ledger: specs append one entry per Task they run; the teardown reads
// them all and prints the total, so a run always ends with what it cost
// (PLAN.md §19: actual spend is reported after each live run).
export interface SpendEntry {
  task: string; // the prompt
  costUsd: number | null; // null when the model has no price in prices.yaml
  costKnown: boolean;
}

export function recordSpend(entry: SpendEntry): void {
  ensureLiveArtifacts();
  const ledger = readSpend();
  ledger.push(entry);
  writeFileSync(SPEND_FILE, JSON.stringify(ledger, null, 2));
}

export function readSpend(): SpendEntry[] {
  if (!existsSync(SPEND_FILE)) return [];
  try {
    return JSON.parse(readFileSync(SPEND_FILE, "utf8")) as SpendEntry[];
  } catch {
    return [];
  }
}

export function resetSpend(): void {
  ensureLiveArtifacts();
  writeFileSync(SPEND_FILE, JSON.stringify([], null, 2));
}

// parseCost pulls the dollar amount out of the Agent window's meta line, which
// reads like "1,234 in · 567 out · $0.0123" (see apps/agent/format.ts costLine).
// Returns null when the model is unpriced ("cost unknown").
export function parseCost(meta: string): { costUsd: number | null; costKnown: boolean } {
  const m = meta.match(/\$([0-9]+(?:\.[0-9]+)?)/);
  if (!m) return { costUsd: null, costKnown: false };
  return { costUsd: Number(m[1]), costKnown: true };
}

// Everything provider-agnostic comes straight from the fake suite's harness, so
// the two suites drive the Desktop through exactly the same helpers.
export {
  docker,
  exec,
  sh,
  signIn,
  mintCode,
  openApp,
  openSpotlight,
  startTask,
  test,
  expect,
  REPO_ROOT,
  type Compose,
} from "../e2e/harness";
