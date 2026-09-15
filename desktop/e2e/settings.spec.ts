// System Settings (PLAN.md §4.3, §18, M4.5): the panes that configure the running
// Machine. This opens the app from Spotlight (it is not in the Dock), walks the
// panes, and exercises the two writes that are safe to make and undo on the
// shared test Machine: saving then resetting the Agent's Autonomy, and adding
// then removing a Protected Path. The fuller behavioural checks — a remapped
// shortcut firing, a Memory proposal accepted, a masked key, a changed Autonomy
// reaching the next Task — live in settings-behaviour.spec.ts (M4.7).
import { clearLayout, expect, exec, loadCompose, openViaSpotlight, sh, test } from "./harness";

// A singleton System Settings window left open by an earlier spec would otherwise
// be re-focused on whatever pane it last showed; start from an empty desktop so
// this opens a fresh window on its default (Agent) pane.
test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("System Settings shows the panes and saves a setting", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "System Settings");
  const win = page.locator('.window[aria-label="System Settings"]').last();

  // Agent is the default pane: the model row loads from the server.
  await expect(win.locator(".set__title", { hasText: "Agent" })).toBeVisible();
  const autonomy = win.getByLabel("Autonomy", { exact: true });
  await expect(autonomy).toBeVisible({ timeout: 10_000 });

  // Save Autonomy, see it marked as saved here, then reset it back.
  const row = win.locator(".set__row", { hasText: "Autonomy" });
  await autonomy.selectOption("confirm-all");
  await expect(row.locator(".set__badge--saved")).toBeVisible({ timeout: 10_000 });
  await row.getByRole("button", { name: "Reset" }).click();
  await expect(row.locator(".set__badge--saved")).toHaveCount(0, { timeout: 10_000 });

  // Protected Paths: lock a real folder in the home (Protect requires it to
  // exist and be inside home), see it listed as yours with Remove, remove it.
  const c = loadCompose();
  const dir = `pw-protect-${Date.now()}`;
  const path = `/home/aos/${dir}`;
  sh(c, "aos", `mkdir -p ~/${dir}`);
  try {
    await win.locator(".set__navitem", { hasText: "Protected Paths" }).click();
    await win.getByLabel("Path to protect").fill(path);
    await win.getByRole("button", { name: "Lock path" }).click();
    const entry = win.locator(".prot__item", { hasText: path });
    await expect(entry).toBeVisible({ timeout: 10_000 });
    await entry.getByRole("button", { name: "Remove" }).click();
    await expect(win.locator(".prot__item", { hasText: path })).toHaveCount(0, { timeout: 10_000 });
  } finally {
    exec(c, "aos", "rm", "-rf", path);
  }

  // API key: the current key shows only as a hint (or "no key"), never a field
  // pre-filled with a secret.
  await win.locator(".set__navitem", { hasText: "API key" }).click();
  await expect(win.getByLabel("New API key")).toHaveValue("");

  // Memory loads its list without error.
  await win.locator(".set__navitem", { hasText: "Memory" }).click();
  await expect(win.getByLabel("New memory")).toBeVisible();

  // Keyboard: record a new combo for Switch Window (a shortcut the helpers don't
  // use), see the button take it, then Restore defaults and see it revert.
  await win.locator(".set__navitem", { hasText: "Keyboard" }).click();
  const combo = win.getByLabel("Shortcut for Switch Window");
  await expect(combo).toBeVisible();
  await expect(combo).not.toContainText("J");
  await combo.click();
  await page.keyboard.press("Alt+J");
  await expect(combo).toContainText("J", { timeout: 5_000 });
  await win.getByRole("button", { name: "Restore defaults" }).click();
  await expect(combo).not.toContainText("J");

  // Appearance: switching to Dark applies the theme to the Desktop; put it back.
  await win.locator(".set__navitem", { hasText: "Appearance" }).click();
  await win.locator(".appr__choice", { hasText: "Dark" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await win.locator(".appr__choice", { hasText: "Auto" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", "dark");

  // Liquid Glass: the toggle marks the Desktop (data-glass), off by default; the
  // watchdog turning it off from dropped frames is covered in glass.spec.ts. Leave
  // it off here.
  const glass = win.getByRole("switch", { name: "Liquid Glass" });
  await expect(glass).toHaveAttribute("aria-checked", "false");
  await glass.click();
  await expect(page.locator("html")).toHaveAttribute("data-glass", "on");
  await glass.click();
  await expect(page.locator("html")).not.toHaveAttribute("data-glass", "on");

  // Status reports the Machine's Mode read-only.
  await win.locator(".set__navitem", { hasText: "Status" }).click();
  await expect(win.locator(".status__facts")).toContainText("Mode");
});
