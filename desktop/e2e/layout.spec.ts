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
