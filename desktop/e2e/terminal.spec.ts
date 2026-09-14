// Terminal (PLAN.md §4.3, §10, §18, M3.3): a User Session runs commands as aos.
// The xterm buffer is read through the test-only hook, so the check is renderer-
// independent (WebGL draws to a canvas with no DOM text).
import { expect, openApp, test } from "./harness";
import { termText } from "./term-helpers";

test("a User Session runs a command as aos", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Terminal");

  // A reload may have restored earlier Terminal windows; drive the one we just
  // opened (the newest, which is also the terminal the test hook exposes).
  const term = page.locator('.window[aria-label="Terminal"]').last();
  await expect(term.locator(".term__pane .xterm")).toBeVisible();
  await page.waitForFunction(() => Boolean((window as unknown as { __aosTerm?: unknown }).__aosTerm));

  await term.locator(".term__pane").click();
  // whoami confirms the Session runs as aos; the marker confirms our keystrokes
  // reached the PTY and its output came back into xterm.
  await page.keyboard.type("whoami && echo term-ok-$(id -un)");
  await page.keyboard.press("Enter");

  await expect
    .poll(() => termText(page), { timeout: 15_000, message: "the terminal never ran the command as aos" })
    .toContain("term-ok-aos");
});
