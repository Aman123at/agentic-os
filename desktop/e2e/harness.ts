// Shared plumbing for the Desktop e2e suite (PLAN.md §17, M3.5): the fixed test
// URL, the Docker Compose invocation and small helpers the specs and the
// setup/teardown share. The Machine itself is started in global-setup.ts.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

import { test as base, expect, type BrowserContext, type Locator, type Page } from "@playwright/test";

// A Host port of our own, apart from the user's Machine (7700) and tools/e2e
// (7793). The Host guard accepts 127.0.0.1, and the browser sends a matching
// Origin, so sign-in and every RPC are allowed.
export const PORT = "7794";
export const BASE_URL = `http://127.0.0.1:${PORT}`;

const here = path.dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = path.resolve(here, "..", "..");
export const ARTIFACTS = path.join(here, ".artifacts");
export const STATE_FILE = path.join(ARTIFACTS, "state.json");
const COMPOSE_FILE = path.join(ARTIFACTS, "compose.json");

export function ensureArtifacts(): void {
  if (!existsSync(ARTIFACTS)) mkdirSync(ARTIFACTS, { recursive: true });
}

// Compose describes how to talk to the Machine global-setup started.
export interface Compose {
  project: string;
  envFile: string;
  files: string[]; // compose.yaml, then the override
  dir: string; // the scratch folder to clean up
  env: Record<string, string>; // the variables the compose CLI needs
}

export function loadCompose(): Compose {
  return JSON.parse(readFileSync(COMPOSE_FILE, "utf8")) as Compose;
}

export function saveCompose(c: Compose): void {
  ensureArtifacts();
  writeFileSync(COMPOSE_FILE, JSON.stringify(c, null, 2));
}

// docker runs `docker compose … args` against the test Machine and returns its
// stdout. The compose variables are passed both as --env-file (for ${…}
// substitution) and in the process environment (for the secret's OPENAI_API_KEY).
export function docker(c: Compose, args: string[], input?: string): string {
  const base = ["compose", "-p", c.project, "--env-file", c.envFile];
  for (const f of c.files) base.push("-f", f);
  return execFileSync("docker", [...base, ...args], {
    cwd: REPO_ROOT,
    env: { ...process.env, ...c.env },
    encoding: "utf8",
    input,
    maxBuffer: 64 * 1024 * 1024,
  });
}

// exec runs a command in the Machine as user and returns its combined output.
export function exec(c: Compose, user: string, ...cmd: string[]): string {
  return docker(c, ["exec", "-T", "-u", user, "aos", ...cmd]);
}

// sh runs a shell script in the Machine as user.
export function sh(c: Compose, user: string, script: string): string {
  return exec(c, user, "bash", "-c", script);
}

// PLAN.md §16 targets the suite checks besides its performance specs: `docker
// compose up` (image built) to the Desktop's first paint, and aosd's memory
// once idle for IDLE_MS.
export const COMPOSE_UP_TARGET_MS = 5_000;
export const IDLE_RSS_TARGET_MB = 50;
export const IDLE_MS = 10_000;

// aosdRssMB reads aosd's resident memory. aosd is the oldest process of that
// name: Compose's init is PID 1, and aosd's helpers start after it.
export function aosdRssMB(c: Compose): number {
  const out = sh(c, "root", "grep VmRSS /proc/$(pgrep -o -x aosd)/status");
  const m = out.match(/VmRSS:\s+(\d+)\s+kB/);
  if (!m) throw new Error(`no VmRSS for aosd in: ${out}`);
  return Number(m[1]) / 1024;
}

// checkIdleMemory fails when aosd's memory is over the §16 target.
export function checkIdleMemory(c: Compose, when: string): void {
  const mb = aosdRssMB(c);
  console.log(`[pw] §16 aosd idle memory ${when}: ${mb.toFixed(1)} MB RSS (target < ${IDLE_RSS_TARGET_MB} MB)`);
  if (mb >= IDLE_RSS_TARGET_MB) {
    throw new Error(`aosd uses ${mb.toFixed(1)} MB when idle ${when}; the target is under ${IDLE_RSS_TARGET_MB} MB (PLAN.md §16)`);
  }
}

// mintCode asks the Machine for a fresh one-time sign-in code (`aos desktop-url`
// prints a URL with #code=…). Each code works once.
export function mintCode(c: Compose): string {
  const out = docker(c, ["exec", "-T", "aos", "aos", "desktop-url"]);
  const m = out.match(/#code=([^\s&]+)/);
  if (!m) throw new Error(`no #code in desktop-url output:\n${out}`);
  return m[1];
}

// signIn drives the sign-in the way a person following the link would: open the
// Desktop with the code in the hash, then wait for the shell. Returns once ready.
export async function signIn(page: Page, code: string): Promise<void> {
  await page.goto(`/#code=${code}`);
  await expect(page.locator(".desktop")).toBeVisible({ timeout: 20_000 });
}

// openSpotlight opens the launcher with the Host-appropriate shortcut (Alt+Space
// on mac/Linux, Ctrl+Space on Windows), retrying with the other in case a CI
// browser reports an unexpected platform.
export async function openSpotlight(page: Page): Promise<void> {
  // Move focus off any window input (a restored Terminal's xterm would otherwise
  // swallow the shortcut) so the window-level keydown handler sees it.
  await page.locator(".menubar__app").click().catch(() => {});
  const input = page.getByLabel("Spotlight search");
  await page.keyboard.press("Alt+Space");
  if (await input.isVisible().catch(() => false)) return;
  await page.keyboard.press("Control+Space");
  await expect(input).toBeVisible();
}

// openViaSpotlight opens an app that is not in the Dock (Activity Monitor,
// Software) the way a user would: Spotlight, type its name, click the app row.
export async function openViaSpotlight(page: Page, name: string): Promise<void> {
  await openSpotlight(page);
  const input = page.getByLabel("Spotlight search");
  await input.fill(name);
  await page.locator(".spot__row").filter({ hasText: name }).first().click();
  await expect(page.locator(`.window[aria-label="${name}"]`).last()).toBeVisible();
}

// startTask hands a prompt to the Agent through Spotlight, the way a user would,
// and waits for the Tasks surface to open on it.
export async function startTask(page: Page, prompt: string): Promise<void> {
  await openSpotlight(page);
  const input = page.getByLabel("Spotlight search");
  await input.fill(prompt);
  await expect(page.getByText(`Ask the Agent: “${prompt}”`)).toBeVisible();
  await input.press("Enter");
  await expect(page.locator(".tasks__title")).toBeVisible();
}

// clearLayout makes a context's pages open on an empty desktop, ignoring the
// windows earlier specs left in the shared server state. The same reset perf.spec
// uses, for specs that need a known starting scene rather than whatever debris the
// serial suite has accumulated. Call it from a beforeEach.
export async function clearLayout(context: BrowserContext): Promise<void> {
  await context.addInitScript(() => {
    try {
      sessionStorage.setItem("aos.layout", JSON.stringify({ theme: "auto", windows: [], focused: "" }));
    } catch {
      // Without storage the page restores the server layout; the spec still runs.
    }
  });
}

// openApp opens an app from the Dock by its name and waits for its (newest)
// window. A reload may have restored earlier windows of the same app, so callers
// that drive the window should target `.last()`.
export async function openApp(page: Page, name: string): Promise<void> {
  await page.locator(`.dock__tile[title="${name}"]`).click();
  await expect(page.locator(`.window[aria-label="${name}"]`).last()).toBeVisible();
}

// The e2e `test`: every context gets the harness flag before any script runs, so
// the test-only terminal hook (see apps/terminal/term.ts) is available.
// The pane dividers (PLAN.md M5.1), shared by the specs that pull them.
export const paneWidth = async (l: Locator) => Math.round((await l.boundingBox())!.width);

// A window plays a 140 ms scale-in when it mounts and after a reload; a box
// measured mid-flight is the scaled one, not the laid-out one.
export async function settled(win: Locator): Promise<void> {
  await win.evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished.catch(() => {}))));
}

// dragDivider pulls a divider dx pixels sideways, as a pointer does.
export async function dragDivider(page: Page, divider: Locator, dx: number): Promise<void> {
  const box = (await divider.boundingBox())!;
  const y = box.y + box.height / 2;
  await page.mouse.move(box.x + box.width / 2, y);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + dx, y, { steps: 8 });
  await page.mouse.up();
}

export const test = base.extend({
  context: async ({ context }, use) => {
    await context.addInitScript(() => {
      (window as unknown as { __AOS_E2E__?: boolean }).__AOS_E2E__ = true;
    });
    await use(context);
  },
});

export { expect };
