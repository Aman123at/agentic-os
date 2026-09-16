// Reload restores the window layout (PLAN.md §18, M3.1). Each tab keeps its own
// layout, so a reload brings back that tab's windows rather than another tab's,
// and each window's content comes back with it: the Task the Agent app
// showed and the folder Finder was in. A new tab starts from the layout saved on
// aosd (debounced SaveDesktopState).
import type { Page } from "@playwright/test";

import { exec, expect, loadCompose, openApp, sh, startTask, test } from "./harness";

const windows = (p: Page, app: string) => p.locator(`.window[aria-label="${app}"]`);

test("windows come back after a reload", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  await openApp(page, "Terminal");

  // Let the debounced SaveDesktopState (400 ms) reach the server.
  await page.waitForTimeout(900);
  await page.reload();

  await expect(windows(page, "Finder").first()).toBeVisible();
  await expect(windows(page, "Terminal").first()).toBeVisible();
});

test("a reload brings back the open Task and Finder's folder", async ({ page }) => {
  const c = loadCompose();
  const folder = `pw-restore-${Date.now()}`;
  sh(c, "aos", `mkdir -p ~/${folder}`);
  const prompt = `e2e-ui: restore me ${Date.now()}`;

  await page.goto("/");
  await startTask(page, prompt);
  await expect(page.locator(".tasks__title")).toContainText(prompt);

  await openApp(page, "Finder");
  const finder = windows(page, "Finder").last();
  await finder.getByText(folder).dblclick();
  await expect(finder.locator(".finder__crumb--on")).toHaveText(folder);

  // No wait: the pending save is flushed as the page goes away.
  await page.reload();

  await expect(page.locator(".tasks__title")).toContainText(prompt);
  await expect(windows(page, "Finder").last().locator(".finder__crumb--on")).toHaveText(folder);

  exec(c, "aos", "rm", "-rf", `/home/aos/${folder}`);
});

test("each tab restores its own layout", async ({ page, context }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  const finders = await windows(page, "Finder").count();
  const terminals = await windows(page, "Terminal").count();

  // Another tab changes the layout saved on aosd…
  const other = await context.newPage();
  await other.goto("/");
  await openApp(other, "Terminal");
  await other.waitForTimeout(900);

  // …but reloading the first tab brings back the first tab's own windows.
  await page.reload();
  await expect(page.locator(".menubar")).toBeVisible();
  await expect(windows(page, "Finder")).toHaveCount(finders);
  await expect(windows(page, "Terminal")).toHaveCount(terminals);

  await other.close();
});

test("a minimized window comes back from the Dock", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  const finder = windows(page, "Finder");
  const count = await finder.count();

  await finder.last().locator(".traffic__min").click();
  await expect(finder.last()).toBeHidden();

  // The Dock is the way back: it restores the window rather than opening another
  // (M4.8 8.2). A window with nowhere to go is a window the user has lost.
  await page.locator('.dock__tile[title="Finder"]').click();
  await expect(finder.last()).toBeVisible();
  expect(await finder.count(), "the Dock opened a second window instead of restoring").toBe(count);
});

test("a minimized window survives a reload and can still be restored", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Terminal");
  const terms = windows(page, "Terminal");
  await terms.last().locator(".traffic__min").click();
  await expect(terms.last()).toBeHidden();

  await page.waitForTimeout(900);
  await page.reload();
  await expect(page.locator(".desktop")).toBeVisible();

  await page.locator('.dock__tile[title="Terminal"]').click();
  await expect(windows(page, "Terminal").last()).toBeVisible();
});

// Focus follows the window in front (PLAN.md M4.8 item 8.8). Closing or
// minimizing the front window used to leave nothing focused, so the menu bar
// said "Agentic OS" with a window plainly on screen.
test("closing the front window focuses the one behind it", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  await openApp(page, "Terminal");
  await expect(page.locator(".menubar__app")).toHaveText("Terminal");

  await windows(page, "Terminal").last().locator(".traffic__close").click();
  await expect(page.locator(".menubar__app")).toHaveText("Finder");
});

test("minimizing the front window focuses the one behind it", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  await openApp(page, "Terminal");
  await expect(page.locator(".menubar__app")).toHaveText("Terminal");

  await windows(page, "Terminal").last().locator(".traffic__min").click();
  await expect(windows(page, "Terminal").last()).toBeHidden();
  await expect(page.locator(".menubar__app")).toHaveText("Finder");
});

// A window resizes from every edge and corner, not only the bottom-right
// (PLAN.md M4.8 item 8.10). Dragging the left edge left widens it and moves its
// left side; the right side stays put.
test("a window resizes from its left edge", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  const win = windows(page, "Finder").last();
  // A window scales in over 140 ms; measuring mid-animation gives a box that is
  // a few per cent small.
  await page.waitForTimeout(300);
  const before = (await win.boundingBox())!;

  const handle = win.locator(".window__resize--w");
  await handle.hover();
  await page.mouse.down();
  await page.mouse.move(before.x - 80, before.y + before.height / 2, { steps: 8 });
  await page.mouse.up();

  const after = (await win.boundingBox())!;
  expect(Math.round(after.width - before.width), "the window widened by the drag").toBeGreaterThan(60);
  expect(Math.round(after.x), "its left edge moved with the pointer").toBeLessThan(Math.round(before.x) - 60);
  expect(Math.round(after.x + after.width), "its right edge stayed put").toBeCloseTo(Math.round(before.x + before.width), -1);
});
