// Activity Monitor (PLAN.md §4.3, §18, M4.4): the four tabs over what the Machine
// is doing. This opens the app from Spotlight (it is not in the Dock) and checks
// each tab renders live data — the CPU/Memory graphs, the Processes table, and
// Services & Ports (aosd itself listens on a port).
import { expect, openViaSpotlight, test } from "./harness";

test("Activity Monitor shows metrics, processes and ports", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "Activity Monitor");
  const win = page.locator('.window[aria-label="Activity Monitor"]').last();

  // CPU / Memory tab is the default: the CPU and Memory cards are present, and
  // the Memory card fills in a used/total figure from a real sample.
  await expect(win.locator(".metrics__h", { hasText: "CPU" })).toBeVisible();
  await expect(win.locator(".metrics__card", { hasText: "Memory" }).locator(".metrics__now")).not.toHaveText("…", { timeout: 10_000 });

  // Processes tab: the table lists real processes from the Machine.
  await win.locator(".activity__tab", { hasText: "Processes" }).click();
  await expect(win.locator(".activity__count")).toContainText("processes");
  await expect(win.locator(".procs__row:not(.procs__row--head)").first()).toBeVisible({ timeout: 10_000 });

  // Services & Ports tab: aosd's own listening port shows under listening ports.
  await win.locator(".activity__tab", { hasText: "Services & Ports" }).click();
  await expect(win.locator(".svc__h", { hasText: "Services" })).toBeVisible();
  await expect(win.locator(".svc__h", { hasText: "Other listening ports" })).toBeVisible();
  await expect(win.locator(".svc__port").first()).toBeVisible({ timeout: 10_000 });
});
