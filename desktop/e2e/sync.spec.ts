// Two tabs stay in sync (PLAN.md §18, M3.1): both subscribe to aosd's event
// stream, so a Task started in one tab appears in the other's Notification
// Center. Uses the zero-side-effect ui-hello cassette (no model spend).
import { clearLayout, expect, startTask, test } from "./harness";

// Both tabs start from an empty desktop, so an Agent window an earlier spec left
// in the shared layout can't also list the Task and make the match ambiguous.
test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("a Task started in one tab shows in the other's Notification Center", async ({ page, context }) => {
  const other = await context.newPage();
  await other.goto("/");
  await expect(other.locator(".menubar")).toBeVisible();

  await page.goto("/");
  const prompt = `e2e-ui: say hello ${Date.now()}`;
  await startTask(page, prompt);

  // The other tab, which never created the Task, learns about it over the stream.
  await other.locator(".menubar__bell").click();
  await expect(other.getByRole("dialog", { name: "Notification Center" })).toBeVisible();
  await expect(other.getByText(prompt)).toBeVisible();
});

test("a preference changed in one tab reaches the other", async ({ page, context }) => {
  await page.goto("/");
  await expect(page.locator(".menubar")).toBeVisible();
  const other = await context.newPage();
  await other.goto("/");
  await expect(other.locator(".menubar")).toBeVisible();

  // Appearance is the Machine's, not the tab's: the menu bar's theme button
  // cycles it, and the other tab follows over the event stream (M4.8 8.4).
  const themeButton = (p: typeof page) => p.locator('.menubar__item[title="Appearance"]');
  // Both tabs start from the cleared layout, so both start on Auto.
  await expect(themeButton(other)).toHaveText("Auto");
  await themeButton(page).click();
  await expect(themeButton(page)).toHaveText("Light");
  await expect(themeButton(other)).toHaveText("Light");
  await expect(other.locator("html")).toHaveAttribute("data-theme", "light");
});
