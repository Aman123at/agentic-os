// Reload restores the window layout (PLAN.md §18, M3.1): the layout is saved to
// aosd (debounced) and GetDesktopState restores it on the next load.
import { expect, openApp, test } from "./harness";

test("windows come back after a reload", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Finder");
  await openApp(page, "Terminal");

  // Let the debounced SaveDesktopState (400 ms) reach the server.
  await page.waitForTimeout(900);
  await page.reload();

  await expect(page.locator('.window[aria-label="Finder"]').first()).toBeVisible();
  await expect(page.locator('.window[aria-label="Terminal"]').first()).toBeVisible();
});
