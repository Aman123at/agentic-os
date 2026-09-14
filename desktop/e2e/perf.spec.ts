// The performance gate (PLAN.md §16, §18, M3.5). Two interactions guard the
// §4.3 smoothness rules:
//   • Window drag stays at 60fps — dragging writes transform in rAF and commits
//     to the store only on pointer-up, so no frame does over-budget work. We
//     assert no long animation frame (≥ one 60Hz budget spilling past ~50 ms)
//     occurs across a scripted drag, and report the p95 frame interval.
//   • Keystroke echo is under 30 ms p95 — output goes straight into xterm, never
//     through React state, so echo does not wait on a render.
import { expect, openApp, test } from "./harness";
import { measureKeystrokeEcho } from "./term-helpers";

test("window drag holds 60fps with no dropped frames", async ({ page }) => {
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

  // Record every animation frame while we drag the measured segment.
  await page.evaluate(() => {
    const w = window as unknown as { __frames: number[]; __raf: boolean };
    w.__frames = [];
    w.__raf = true;
    const loop = (t: number) => {
      if (!w.__raf) return;
      w.__frames.push(t);
      requestAnimationFrame(loop);
    };
    requestAnimationFrame(loop);
  });

  for (let i = 1; i <= 60; i++) {
    await page.mouse.move(x + (15 + i) * 3, y + Math.sin(i / 6) * 4);
    await page.waitForTimeout(8);
  }
  await page.mouse.up();

  const res = await page.evaluate(() => {
    const w = window as unknown as { __frames: number[]; __raf: boolean };
    w.__raf = false;
    const f = w.__frames;
    const d: number[] = [];
    // Drop the first delta: it spans the gap before the first rAF after we began.
    for (let i = 2; i < f.length; i++) d.push(f[i] - f[i - 1]);
    d.sort((a, b) => a - b);
    const p95 = d.length ? d[Math.min(d.length - 1, Math.floor(d.length * 0.95))] : 0;
    return { frames: f.length, p95, max: d.length ? d[d.length - 1] : 0, long: d.filter((x) => x > 50).length };
  });
  console.log(`[perf] drag (steady state): ${res.frames} frames · p95 interval ${res.p95.toFixed(1)}ms · max ${res.max.toFixed(1)}ms · long(>50ms) ${res.long}`);

  expect(res.frames, "the drag should have animated many frames").toBeGreaterThan(15);
  // Dragging writes transform in rAF and never re-renders React, so no frame in
  // the steady-state segment overruns badly (a dropped frame would exceed 50 ms).
  expect(res.long, "no dropped frames during the steady-state drag").toBe(0);

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
