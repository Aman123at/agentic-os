// The Agent app against a real model (PLAN.md §4.3, §16, M4.7). Unlike the fake
// suite (e2e/agent.spec.ts), which asserts the exact words a cassette replays,
// this one asserts structure a real model must satisfy: a step appears fast, the
// Task reaches Done, the file it was told to write really exists on the Machine,
// and a cost is reported. It spends the key, so it runs only in the live suite.
import type { Page } from "@playwright/test";

import {
  expect,
  loadLiveCompose,
  openSpotlight,
  parseCost,
  recordSpend,
  sh,
  test,
} from "./live-harness";

const agent = (p: Page) => p.locator('.window[aria-label="Agent"]');

test("a real Task streams a first step under 1s, finishes, writes its file and reports a cost", async ({ page }) => {
  const c = loadLiveCompose();
  const notePath = "/home/aos/live-note.txt";
  sh(c, "aos", `rm -f ${notePath}`);

  // A deliberately small, benign Task: one file write, so the run is cheap and
  // its side effect is easy to check. The path is spelled out so the assertion
  // does not depend on the model's phrasing.
  const prompt = `live: write a short two-line hello note to the file ${notePath} (create it) ${Date.now()}`;

  await page.goto("/");
  await openSpotlight(page);
  const input = page.getByLabel("Spotlight search");
  await input.fill(prompt);
  await expect(page.getByText(`Ask the Agent: “${prompt}”`)).toBeVisible();

  // PLAN.md §16: the first visible Agent step lands under 1 s after submit. Time
  // from the Enter that submits to the first step row appearing in the feed.
  const win = agent(page);
  const firstStep = win.locator(".tasks__feed .tasks__step").first();
  const t0 = Date.now();
  await input.press("Enter");
  await expect(win.locator(".tasks__title")).toBeVisible();
  await expect(firstStep).toBeVisible({ timeout: 20_000 });
  const firstStepMs = Date.now() - t0;
  console.log(`[live] first visible Agent step: ${firstStepMs} ms (target < 1000 ms)`);
  expect(firstStepMs, "first visible Agent step is under 1 s (PLAN.md §16)").toBeLessThan(1000);

  // The Task runs to completion against the real model.
  await expect(win.locator(".tasks__state")).toHaveText("Done", { timeout: 120_000 });

  // The side effect is real: the note exists on the Machine and is not empty.
  const note = sh(c, "aos", `cat ${notePath} 2>/dev/null || true`);
  expect(note.trim().length, `the Agent actually created ${notePath}`).toBeGreaterThan(0);

  // A real cost is shown, and we record it for the run's spend report.
  const meta = (await win.locator(".tasks__meta").innerText()).replace(/\s+/g, " ");
  console.log(`[live] Task usage: ${meta}`);
  // The token counts carry a cached total when the provider reports one
  // ("15,065 in (7,421 cached) · 87 out"), so match the parts, not the spacing.
  expect(meta, "the meta line shows tokens and a cost").toMatch(/[\d,]+ in\b.*[\d,]+ out\b.*\$[0-9.]+/);
  recordSpend({ task: prompt, ...parseCost(meta) });
});
