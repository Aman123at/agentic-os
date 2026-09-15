// The performance gate (PLAN.md §16, §18, M3.5). Two interactions guard the
// §4.3 smoothness rules:
//   • Window drag stays at 60fps — dragging writes transform in rAF and commits
//     to the store only on pointer-up, so no frame does over-budget work. A Chrome
//     trace of a scripted drag must show p95 main-thread frame cost < 16.7 ms and
//     no frame dropped or presented without its update (see trace.ts).
//   • Keystroke echo is under 30 ms p95 — output goes straight into xterm, never
//     through React state, so echo does not wait on a render.
import { expect, openApp, test } from "./harness";
import { measureKeystrokeEcho } from "./term-helpers";
import { FRAME_CATEGORIES, frameStats } from "./trace";

test("window drag holds 60fps with no dropped frames", async ({ page, browser }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  // Drive the window we just opened (newest, on top), not one a reload restored.
  const win = page.locator('.window[aria-label="Finder"]').last();
  const bar = win.locator(".window__bar");
  const before = await win.boundingBox();
  const box = await bar.boundingBox();
  if (!before || !box) throw new Error("Finder window not found");

  // Let the window's 140 ms open animation finish before we measure.
  await page.waitForTimeout(400);

  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  // Warm up (compositor/layout) before the measured segment.
  for (let i = 1; i <= 15; i++) await page.mouse.move(x + i * 3, y);

  // Trace the measured segment, bracketed by marks so setup frames don't count.
  await browser.startTracing(page, { categories: FRAME_CATEGORIES });
  await page.evaluate(() => performance.mark("aos-drag-start"));
  for (let i = 1; i <= 60; i++) {
    await page.mouse.move(x + (15 + i) * 3, y + Math.sin(i / 6) * 4);
    await page.waitForTimeout(8);
  }
  await page.evaluate(() => performance.mark("aos-drag-end"));
  await page.mouse.up();
  const stats = frameStats(await browser.stopTracing(), "aos-drag-start", "aos-drag-end");
  console.log(
    `[perf] drag: ${stats.frames} frames · p95 frame ${stats.p95.toFixed(2)}ms · max ${stats.max.toFixed(2)}ms · missed ${stats.missed}`,
  );

  // Every move asks for a frame, so the drag must have rendered many of them.
  expect(stats.frames, "the drag should have rendered many frames").toBeGreaterThan(30);
  expect(stats.p95, "p95 main-thread frame cost fits a 60 Hz frame").toBeLessThan(16.7);
  expect(stats.missed, "no frame dropped or presented without its update").toBe(0);

  // The window actually moved (the drag was real, and committed on pointer-up).
  const after = await win.boundingBox();
  expect(after!.x).toBeGreaterThan(before.x + 100);
});

test("keystroke echo is under 30ms p95", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Terminal");
  const term = page.locator('.window[aria-label="Terminal"]').last();
  await expect(term.locator(".term__pane .xterm")).toBeVisible();
  await page.waitForFunction(() => Boolean((window as unknown as { __aosTerm?: unknown }).__aosTerm));
  await term.locator(".term__pane").click();
  await page.waitForTimeout(600); // let the prompt settle

  const samples = await measureKeystrokeEcho(page, 20);
  expect(samples.length, "collected enough keystroke samples").toBeGreaterThan(8);
  samples.sort((a, b) => a - b);
  const p95 = samples[Math.min(samples.length - 1, Math.floor(samples.length * 0.95))];
  const max = samples[samples.length - 1];
  console.log(`[perf] keystroke echo: n=${samples.length} · p95 ${p95.toFixed(1)}ms · max ${max.toFixed(1)}ms`);

  expect(p95).toBeLessThan(30);
});
