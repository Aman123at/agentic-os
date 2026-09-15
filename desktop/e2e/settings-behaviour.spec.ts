// System Settings behaviour (PLAN.md §4.3, §18, M4.7): the panes don't just
// render (settings.spec.ts covers that) — a saved setting changes what the Machine
// does. This drives the four §18 behaviours that need a Task or a real effect:
//   • a changed Autonomy applies to the next Task;
//   • a key set here shows only as a masked hint, never the secret;
//   • a Memory an Agent proposes can be accepted;
//   • a remapped shortcut fires its action.
// Everything is driven by the fake provider (ui-autonomy.json, ui-memory.json), so
// there is no spend, and each test resets what it changed so the shared Machine is
// left as it was for the specs that follow.
import type { Page } from "@playwright/test";

import { clearLayout, expect, loadCompose, openApp, openViaSpotlight, sh, startTask, test } from "./harness";

test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

const settingsWin = (p: Page) => p.locator('.window[aria-label="System Settings"]').last();
const agentWin = (p: Page) => p.locator('.window[aria-label="Agent"]').last();

// resetSetting clears a saved setting back to its env/default value, straight
// through the RPC, so cleanup runs even if the UI is mid-change (an empty value
// clears the saved one — see SettingsService.Update).
async function resetSetting(page: Page, key: string): Promise<void> {
  await page
    .evaluate(async (k) => {
      await fetch("/aos.v1.SettingsService/Update", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: k, value: "" }),
      });
    }, key)
    .catch(() => {});
}

test("a changed Autonomy applies to the next Task", async ({ page }) => {
  const c = loadCompose();
  sh(c, "aos", "rm -rf ~/ui-autonomy");
  await page.goto("/");
  try {
    // Raise Autonomy to confirm-all in System Settings.
    await openViaSpotlight(page, "System Settings");
    const win = settingsWin(page);
    const autonomy = win.getByLabel("Autonomy", { exact: true });
    await expect(autonomy).toBeVisible({ timeout: 10_000 });
    await autonomy.selectOption("confirm-all");
    const row = win.locator(".set__row", { hasText: "Autonomy" });
    await expect(row.locator(".set__badge--saved")).toBeVisible({ timeout: 10_000 });

    // A plain write the default confirm-risky would allow silently now needs an
    // Approval — proof the new Autonomy took effect for this Task, not the path.
    await startTask(page, `ui-autonomy: jot a quick note ${Date.now()}`);
    const inline = agentWin(page).getByRole("group", { name: "Approval for this Task" });
    await expect(inline).toBeVisible();
    await expect(inline).toContainText(/confirm-all/i);
    await inline.getByRole("button", { name: "Allow once" }).click();

    await expect(agentWin(page).locator(".tasks__state")).toHaveText("Done");
    expect(sh(c, "aos", "cat ~/ui-autonomy/note.txt").trim()).toBe("A quick note.");
  } finally {
    await resetSetting(page, "autonomy");
    sh(c, "aos", "rm -rf ~/ui-autonomy");
  }
});

test("a key set in System Settings shows only as a masked hint", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "System Settings");
  const win = settingsWin(page);
  await win.locator(".set__navitem", { hasText: "API key" }).click();
  const field = win.getByLabel("New API key");
  await expect(field).toHaveValue("");
  try {
    await field.fill("sk-pwtest-0123456789abcdef");
    await win.getByRole("button", { name: "Replace key" }).click();

    // Only a hint comes back: masked, ending in the key's last characters — never
    // the secret itself — and the field is cleared.
    const hint = win.locator(".apikey__hint");
    await expect(hint).toBeVisible();
    await expect(hint).toContainText("sk-");
    await expect(hint).not.toContainText("0123456789abcdef");
    await expect(win.locator(".apikey__now")).toContainText("Set here");
    await expect(field).toHaveValue("");
  } finally {
    // Revert to the .env key so the Agent specs keep their (dummy) provider key.
    const revert = win.getByRole("button", { name: "Use key from .env" });
    if (await revert.isVisible().catch(() => false)) await revert.click();
  }
});

test("a Memory an Agent proposes can be accepted", async ({ page }) => {
  const memory = "The user prefers two-space indentation.";
  await page.goto("/");
  await startTask(page, `ui-memory: note my editor preference ${Date.now()}`);
  await expect(agentWin(page).locator(".tasks__state")).toHaveText("Done");

  await openViaSpotlight(page, "System Settings");
  const win = settingsWin(page);
  await win.locator(".set__navitem", { hasText: "Memory" }).click();
  const proposed = win.locator(".mem__item--proposed", { hasText: memory });
  await expect(proposed).toBeVisible({ timeout: 10_000 });
  try {
    await proposed.getByRole("button", { name: "Accept" }).click();
    // It leaves the proposals and joins what is remembered.
    await expect(win.locator(".mem__item--proposed", { hasText: memory })).toHaveCount(0);
    await expect(win.locator(".mem__item:not(.mem__item--proposed)", { hasText: memory })).toBeVisible();
  } finally {
    // Forget it so the Machine's Memory is clean for later runs.
    const forget = win.locator(".mem__item", { hasText: memory }).getByRole("button", { name: /Forget|Dismiss/ });
    if (await forget.first().isVisible().catch(() => false)) await forget.first().click();
  }
});

test("a remapped shortcut fires its action", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "System Settings");
  const win = settingsWin(page);
  await win.locator(".set__navitem", { hasText: "Keyboard" }).click();
  const combo = win.getByLabel("Shortcut for Close Window");
  await expect(combo).toBeVisible();
  try {
    // Record Alt+J for Close Window.
    await combo.click();
    await page.keyboard.press("Alt+J");
    await expect(combo).toContainText("J", { timeout: 5_000 });

    // Open a Finder window — now the focused, top window — and close it with the
    // new combo.
    await openApp(page, "Finder");
    const before = await page.locator('.window[aria-label="Finder"]').count();
    await page.keyboard.press("Alt+J");
    await expect(page.locator('.window[aria-label="Finder"]')).toHaveCount(before - 1);
  } finally {
    await win.getByRole("button", { name: "Restore defaults" }).click();
  }
});
