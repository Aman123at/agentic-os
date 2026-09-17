// The pane dividers (PLAN.md M5.1): the Agent app's sidebar and Task list, and
// Finder's Places sidebar, are dragged to a new width, collapsed and restored,
// and moved from the keyboard. Widths are kept with the window, so a reload
// brings them back. No provider work, so no spend.
import { dragDivider as drag, expect, openApp, paneWidth as width, settled, test } from "./harness";

test("the Agent app's panes are resized, collapsed and restored, and the widths survive a reload", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Agent");
  const win = page.locator('.window[aria-label="Agent"]');
  const list = win.locator(".agent__listpane");
  const nav = win.locator(".agent__nav");
  const listSplit = win.getByRole("separator", { name: "Resize the Tasks list" });
  await settled(win);

  expect(await width(list)).toBe(260);

  // A drag widens the list, and the detail beside it gives up the room.
  await drag(page, listSplit, 100);
  expect(await width(list)).toBe(360);
  await expect(listSplit).toHaveAttribute("aria-valuenow", "360");

  // The width is kept with the window, like the Task and the filter.
  await page.waitForTimeout(900);
  await page.reload();
  await expect(win.locator(".agent__listpane")).toBeVisible();
  await settled(win);
  expect(await width(win.locator(".agent__listpane"))).toBe(360);

  // Past its minimum the list collapses; the divider stays as a handle, and a
  // click on it brings the list back to the width it had.
  await drag(page, win.getByRole("separator", { name: "Resize the Tasks list" }), -400);
  await expect(win.locator(".agent__listpane")).toHaveClass(/agent__listpane--off/);
  const handle = win.getByRole("separator", { name: "Resize the Tasks list" });
  await expect(handle).toHaveClass(/split--off/);
  await handle.click();
  expect(await width(win.locator(".agent__listpane"))).toBe(360);

  // The sidebar has its own divider, and the keyboard moves it.
  const navSplit = win.getByRole("separator", { name: "Resize the Agent views sidebar" });
  expect(await width(nav)).toBe(150);
  await navSplit.focus();
  await page.keyboard.press("ArrowRight");
  expect(await width(nav)).toBe(166);
  await page.keyboard.press("End");
  expect(await width(nav)).toBe(320);
  // Enter collapses it and puts it back.
  await page.keyboard.press("Enter");
  expect(await width(nav)).toBe(0);
  await page.keyboard.press("Enter");
  expect(await width(nav)).toBe(320);
});

test("Finder's Places sidebar is resized and the width survives a reload", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  const win = page.locator('.window[aria-label="Finder"]').last();
  const side = win.locator(".finder__sidebar");
  await settled(win);
  expect(await width(side)).toBe(160);

  await drag(page, win.getByRole("separator", { name: "Resize the Places sidebar" }), 60);
  expect(await width(side)).toBe(220);

  await page.waitForTimeout(900);
  await page.reload();
  const back = page.locator('.window[aria-label="Finder"]').last();
  await expect(back).toBeVisible();
  await settled(back);
  expect(await width(page.locator('.window[aria-label="Finder"]').last().locator(".finder__sidebar"))).toBe(220);
});
