// The desktop's right-click menu and the wallpaper picker (PLAN.md §4.3): a
// generated design is chosen from the picker, painted on the real desktop, tuned
// by the hue slider, and kept — like every preference — in the Desktop state, so
// it returns after a reload. Driven with no backend fixtures: the wallpaper lives
// in the client-owned state blob.
import { expect, test } from "./harness";

test("right-clicking the desktop picks a generated wallpaper that survives a reload", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".desktop")).toBeVisible();

  // A left strip clear of any window: our menu opens instead of the browser's.
  await page.locator(".desktop").click({ button: "right", position: { x: 16, y: 320 } });
  await page.getByRole("button", { name: "Change Wallpaper…" }).click();
  const picker = page.getByRole("dialog", { name: "Wallpaper" });
  await expect(picker).toBeVisible();

  // Picking a design paints it on the desktop at once.
  await picker.getByRole("radio", { name: "Nebula" }).click();
  const gen = page.locator(".wallpaper.wp-gen");
  await expect(gen).toHaveAttribute("data-design", "nebula");

  // The hue slider previews live on the real wallpaper. `fill` moves the range
  // the way a drag does — a hand-dispatched `input` event is swallowed by React's
  // value tracking, so the preview would never repaint.
  await picker.getByLabel("Hue").fill("120");
  await expect(gen).toHaveAttribute("style", /--wall-hue:\s*120/);

  await picker.getByRole("button", { name: "Done" }).click();
  await expect(page.getByRole("dialog", { name: "Wallpaper" })).toHaveCount(0);
  await expect(gen).toHaveAttribute("data-design", "nebula");

  // The choice is saved (debounced) and comes back after a reload.
  await page.waitForTimeout(700);
  await page.reload();
  await expect(page.locator(".wallpaper.wp-gen")).toHaveAttribute("data-design", "nebula");
});
