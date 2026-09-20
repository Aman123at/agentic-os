// Root Mode is visible everywhere (PLAN.md §18 M7.10). These specs fake the Realm
// by overriding root_mode in the Info RPC — the boot-time snapshot the whole
// shell reads — so the menu-bar ROOT badge, the Terminal's red strip, the Agent
// app's banner and About This Machine's Realm line all appear iff the Machine is
// in Root Mode, without switching the shared test Machine into the Root Realm.
import type { Page } from "@playwright/test";

import { clearLayout, expect, openViaSpotlight, test } from "./harness";

// fakeRealm makes Info report the given Realm, leaving the rest of the body real.
async function fakeRealm(page: Page, root: boolean): Promise<void> {
  await page.route("**/aos.v1.SystemService/Info", async (route) => {
    const resp = await route.fetch();
    const json = (await resp.json()) as Record<string, unknown>;
    json.rootMode = root;
    await route.fulfill({ response: resp, json });
  });
}

test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("Root Mode shows the badge, banner, Terminal strip and About Realm line", async ({ page }) => {
  await fakeRealm(page, true);
  await page.goto("/");
  await expect(page.locator(".desktop")).toBeVisible();

  // The menu-bar ROOT badge, and clicking it opens the System pane.
  const badge = page.locator(".menubar__root");
  await expect(badge).toBeVisible();
  await badge.click();
  const settings = page.locator('.window[aria-label="System Settings"]').last();
  await expect(settings.locator(".set__title", { hasText: "System" })).toBeVisible();

  // The Agent app's banner.
  await openViaSpotlight(page, "Agent");
  await expect(page.locator(".agent__rootbanner")).toContainText("Agents run as root");

  // The Terminal's red strip.
  await openViaSpotlight(page, "Terminal");
  await expect(page.locator(".term__rootbar")).toBeVisible();

  // About This Machine names the Realm.
  await openViaSpotlight(page, "About This Machine");
  await expect(page.locator(".about__root")).toHaveText("Root Mode");
});

test("Standard Mode hides every Root Mode surface", async ({ page }) => {
  await fakeRealm(page, false);
  await page.goto("/");
  await expect(page.locator(".desktop")).toBeVisible();

  await expect(page.locator(".menubar__root")).toHaveCount(0);

  await openViaSpotlight(page, "Agent");
  await expect(page.locator(".agent__rootbanner")).toHaveCount(0);

  await openViaSpotlight(page, "Terminal");
  await expect(page.locator(".term__rootbar")).toHaveCount(0);

  await openViaSpotlight(page, "About This Machine");
  await expect(page.locator(".about__root")).toHaveCount(0);
});
