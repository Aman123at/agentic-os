// Sign-in (PLAN.md §7.6, §18): a fresh one-time code from `aos desktop-url`
// exchanges for the session and the shell loads; a bad code leaves the boot card
// asking the user to run the command. These run without the saved session.
import { expect, loadCompose, mintCode, signIn, test } from "./harness";

test.use({ storageState: { cookies: [], origins: [] } });

test("a bad code leaves the boot card, not the shell", async ({ page }) => {
  await page.goto("/#code=not-a-real-code");
  await expect(page.locator(".boot__card")).toBeVisible();
  await expect(page.getByText("aos desktop-url")).toBeVisible();
  await expect(page.locator(".desktop")).toHaveCount(0);
});

test("a fresh code signs in and shows the Desktop", async ({ page }) => {
  await signIn(page, mintCode(loadCompose()));
  await expect(page.locator(".menubar")).toBeVisible();
  // The code is single-use: it is scrubbed from the address bar after exchange.
  expect(page.url()).not.toContain("code=");
});
