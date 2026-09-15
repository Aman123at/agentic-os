// Software (PLAN.md §11, §18, M4.4): Packages, the Install Ledger, Checkpoints and
// Replay. This opens the app from Spotlight (it is not in the Dock), takes a
// Checkpoint and sees it appear in the list, and checks the Replay view renders.
import { expect, openViaSpotlight, test } from "./harness";

test("Software takes a checkpoint and shows it", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "Software");
  const win = page.locator('.window[aria-label="Software"]').last();

  // Packages is the default view; its toolbar renders.
  await expect(win.locator(".sw__toolbar")).toBeVisible();

  // Checkpoints: take one and see it in the list.
  await win.locator(".sw__navitem", { hasText: "Checkpoints" }).click();
  const name = `pw-cp-${Date.now()}`;
  await win.getByLabel("New checkpoint name").fill(name);
  await win.getByRole("button", { name: "Take Checkpoint" }).click();
  await expect(win.locator(".ckpt__name", { hasText: name })).toBeVisible({ timeout: 10_000 });

  // Replay view renders (either live progress, or a "no replay" note).
  await win.locator(".sw__navitem", { hasText: "Replay" }).click();
  await expect(win.locator(".replay").or(win.locator(".agent__empty"))).toBeVisible();
});
