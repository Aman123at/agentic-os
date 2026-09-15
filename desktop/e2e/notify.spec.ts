// The notify Tool (PLAN.md §4.3, §18, M4.1): an Agent posts a Desktop
// notification, and open_in_desktop with a port posts one carrying an Open button
// that points at <port>.localhost. Driven by the fake provider (ui-notify.json),
// so there is no spend. The open_in_desktop file case (a report opened straight
// into TextEdit) is covered in textedit.spec.ts.
import { clearLayout, expect, startTask, test } from "./harness";

test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("an Agent's notify posts a notification, and a port offers an Open button", async ({ page }) => {
  await page.goto("/");
  await startTask(page, `ui-notify: tell me when the site is up ${Date.now()}`);

  await page.locator(".menubar__bell").click();
  const nc = page.getByRole("dialog", { name: "Notification Center" });
  await expect(nc).toBeVisible();

  // The notify Tool's own notification: a title and body, no button.
  const site = nc.locator(".nc__item", { hasText: "Site is up" });
  await expect(site).toBeVisible();
  await expect(site).toContainText("It answers on port 8080.");

  // open_in_desktop with a port posts its own notification, with the Open button
  // (to <port>.localhost); we don't follow it.
  const port = nc.locator(".nc__item", { hasText: "Open port 8080" });
  await expect(port).toBeVisible();
  await expect(port.getByRole("button", { name: "Open", exact: true })).toBeVisible();

  // Dismiss both so the Notification Center is clean for the next spec.
  await site.getByRole("button", { name: "Dismiss Site is up" }).click();
  await port.getByRole("button", { name: "Dismiss Open port 8080" }).click();
  await expect(nc.locator(".nc__item", { hasText: "Site is up" })).toHaveCount(0);
  await expect(nc.locator(".nc__item", { hasText: "Open port 8080" })).toHaveCount(0);
});
