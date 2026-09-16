// Terminal (PLAN.md §4.3, §10, §18, M3.3): a User Session runs commands as aos,
// and Watch replays an Agent's Session even after its Task has ended. The xterm
// buffer is read through the test-only hook, so the checks are renderer-
// independent (WebGL draws to a canvas with no DOM text).
import { docker, expect, loadCompose, openApp, sh, startTask, test } from "./harness";
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

test("Watch replays an Agent Session whose Task has ended", async ({ page }) => {
  const c = loadCompose();
  // The ui-watch cassette runs one command in the Agent's Session (no spend).
  const prompt = `e2e-ui-watch: print the marker ${Date.now()}`;
  await page.goto("/");
  await startTask(page, prompt);
  await expect(page.locator(".tasks__state")).toHaveText("Done");

  const line = docker(c, ["exec", "-T", "aos", "aos", "tasks"])
    .split("\n")
    .find((l) => l.includes(prompt));
  const id = line?.match(/t_[0-9a-f]+/)?.[0];
  if (!id) throw new Error(`Task not listed by aos tasks:\n${line}`);

  await openApp(page, "Terminal");
  const term = page.locator('.window[aria-label="Terminal"]').last();
  const watch = term.getByRole("button", { name: /Watch/ });
  const item = term.locator(".term__menu-item", { hasText: id });
  // The Session is listed as ended once its Task has closed it; reopen the menu
  // until it is.
  await expect(async () => {
    await watch.click();
    try {
      await expect(item).toContainText("ended", { timeout: 1_000 });
    } catch (err) {
      await watch.click();
      throw err;
    }
  }).toPass({ timeout: 15_000 });
  await item.click();

  // Attaching replays what the Agent's terminal showed, then reports the end.
  await expect.poll(() => termText(page), { timeout: 15_000 }).toContain("watch-replay-ok");
  await expect.poll(() => termText(page)).toContain("[the session ended]");
});

test("a new Session opens on a clean prompt", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Terminal");
  await page.waitForFunction(() => Boolean((window as unknown as { __aosTerm?: unknown }).__aosTerm));

  // The framing aosd types to synchronise with the shell is internal plumbing
  // (PLAN.md §10); what the user opens on is a prompt, not `__aos_c …`.
  await expect.poll(() => termText(page), { timeout: 10_000, message: "the Session never drew a prompt" }).toContain("aos$");
  expect(await termText(page)).not.toContain("__aos_c");
});

test("a reload re-attaches the Session instead of leaving it running", async ({ page }) => {
  const c = loadCompose();
  const shells = () => Number(sh(c, "root", "ls -d /run/aos/sessions/u_* 2>/dev/null | wc -l").trim());

  await page.goto("/");
  await openApp(page, "Terminal");
  const term = page.locator('.window[aria-label="Terminal"]').last();
  await expect(term.locator(".term__pane .xterm")).toBeVisible();
  await page.waitForFunction(() => Boolean((window as unknown as { __aosTerm?: unknown }).__aosTerm));

  const marker = `reattach-${Date.now()}`;
  await term.locator(".term__pane").click();
  await page.keyboard.type(`echo ${marker}`);
  await page.keyboard.press("Enter");
  await expect.poll(() => termText(page), { timeout: 15_000 }).toContain(marker);
  const before = shells();

  // Let the debounced save record which Session this window has, then reload.
  await page.waitForTimeout(900);
  await page.reload();
  await expect(page.locator('.window[aria-label="Terminal"]').last().locator(".term__pane .xterm")).toBeVisible();
  await page.waitForFunction(() => Boolean((window as unknown as { __aosTerm?: unknown }).__aosTerm));

  // The same shell: its scrollback is replayed, and no second one was started.
  await expect
    .poll(() => termText(page), { timeout: 15_000, message: "the reload did not re-attach the Session" })
    .toContain(marker);
  expect(shells(), "the reload started another shell and orphaned the old one").toBe(before);
});
