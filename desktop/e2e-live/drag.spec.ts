// The real-GPU window-drag run (PLAN.md §16, §18, §22, M4.7). The fake suite's
// perf.spec.ts drags eight windows too, but headless Chromium's software
// compositor can't drop frames the way a GPU does, so there it gates only on
// main-thread p95. This suite runs headed on a real GPU (playwright.live.config
// sets headless:false), so it can assert the stronger rule: with eight-plus
// windows open, no frame is dropped or presented without its main-thread update.
// It calls no model, so it adds nothing to the run's spend.
import { openApp, expect, test } from "./live-harness";
import { FRAME_CATEGORIES, frameStats } from "../e2e/trace";

test.beforeEach(async ({ context }) => {
  await context.addInitScript(() => {
    try {
      sessionStorage.setItem("aos.layout", JSON.stringify({ theme: "auto", windows: [], focused: "" }));
    } catch {
      // Without storage the page restores aosd's layout; the check still runs.
    }
  });
});

test("dragging with eight windows holds 60fps with no dropped frames on a real GPU", async ({ page, browser }) => {
  await page.goto("/");
  // Behind the dragged window, a Terminal's live WebGL canvas, plus eight Finder
  // windows cascaded — a real, busy scene rather than empty frames.
  await openApp(page, "Terminal");
  const WINDOWS = 8;
  for (let i = 0; i < WINDOWS; i++) {
    await page.locator('.dock__tile[title="Finder"]').click();
    await expect(page.locator('.window[aria-label="Finder"]')).toHaveCount(i + 1);
  }
  expect(await page.locator(".window").count()).toBeGreaterThanOrEqual(WINDOWS + 1);

  const win = page.locator('.window[aria-label="Finder"]').last();
  const bar = win.locator(".window__bar");
  const before = await win.boundingBox();
  const box = await bar.boundingBox();
  if (!before || !box) throw new Error("no Finder window to drag");

  // Let the windows' open animations finish before measuring.
  await page.waitForTimeout(500);

  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  for (let i = 1; i <= 15; i++) await page.mouse.move(x + i * 3, y); // warm up

  await browser.startTracing(page, { categories: FRAME_CATEGORIES });
  await page.evaluate(() => performance.mark("aos-live-drag-start"));
  for (let i = 1; i <= 60; i++) {
    await page.mouse.move(x + (15 + i) * 3, y + Math.sin(i / 6) * 4);
    await page.waitForTimeout(8);
  }
  await page.evaluate(() => performance.mark("aos-live-drag-end"));
  await page.mouse.up();
  const stats = frameStats(await browser.stopTracing(), "aos-live-drag-start", "aos-live-drag-end");
  console.log(
    `[live] drag (${await page.locator(".window").count()} windows, real GPU): ${stats.frames} frames · ` +
      `p95 ${stats.p95.toFixed(2)}ms · max ${stats.max.toFixed(2)}ms · missed ${stats.missed} ` +
      `(dropped ${stats.dropped}, partial ${stats.partial})`,
  );

  expect(stats.frames, "the drag should have rendered many frames").toBeGreaterThan(30);
  expect(stats.p95, "p95 main-thread frame cost fits a 60 Hz frame").toBeLessThan(16.7);
  expect(stats.missed, "no frame dropped or presented without its update, even with eight windows").toBe(0);

  const after = await win.boundingBox();
  expect(after!.x).toBeGreaterThan(before.x + 100);
});
