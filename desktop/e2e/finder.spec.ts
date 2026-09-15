// Finder (PLAN.md §4.3, §18, M3.2, M4.3): the Places sidebar and a real listing
// read over FileService — a file created in the Machine shows up in Home — Space
// toggles Quick Look on the selected file, as on macOS, uploads and downloads
// stream through /upload and /files/raw, the column view refreshes live, and a
// file can be protected or handed to the Agent.
import { statSync } from "node:fs";

import type { Page } from "@playwright/test";

import { expect, exec, loadCompose, openApp, sh, test } from "./harness";

async function openHome(page: Page) {
  await page.goto("/");
  await openApp(page, "Finder");
  const finder = page.locator('.window[aria-label="Finder"]').last();
  await finder.locator(".finder__sidebar").getByText("Home").click();
  return finder;
}

test("Finder lists Home and shows a file created in the Machine", async ({ page }) => {
  const c = loadCompose();
  const name = `pw-finder-${Date.now()}.txt`;
  sh(c, "aos", `printf 'hello from playwright\\n' > ~/${name}`);

  await page.goto("/");
  await openApp(page, "Finder");
  const finder = page.locator('.window[aria-label="Finder"]').last();

  // The sidebar Places are always present (icon + name in one button).
  const sidebar = finder.locator(".finder__sidebar");
  for (const place of ["Home", "Downloads", "Shared", "Trash"]) {
    await expect(sidebar.getByText(place)).toBeVisible();
  }

  // Home lists the file we just created.
  await sidebar.getByText("Home").click();
  await expect(finder.getByText(name)).toBeVisible();

  exec(c, "aos", "rm", "-f", `/home/aos/${name}`);
});

test("Space toggles Quick Look on the selected file", async ({ page }) => {
  const c = loadCompose();
  const name = `pw-quicklook-${Date.now()}.txt`;
  sh(c, "aos", `printf 'quick look body\\n' > ~/${name}`);

  await page.goto("/");
  await openApp(page, "Finder");
  const finder = page.locator('.window[aria-label="Finder"]').last();
  await finder.locator(".finder__sidebar").getByText("Home").click();
  await finder.getByText(name).click();

  const preview = page.locator(".quicklook__text");
  await page.keyboard.press("Space");
  await expect(preview).toContainText("quick look body");
  await page.keyboard.press("Space");
  await expect(preview).toBeHidden();

  exec(c, "aos", "rm", "-f", `/home/aos/${name}`);
});

test("Finder uploads through /upload and downloads through /files/raw, past the old 1 MiB chunks", async ({ page }) => {
  const c = loadCompose();
  const dir = `pw-transfer-${Date.now()}`;
  const size = 3 << 20;
  sh(c, "aos", `mkdir -p ~/${dir}`);

  const finder = await openHome(page);
  await finder.getByText(dir).dblclick();
  await expect(finder.locator(".finder__crumb--on")).toHaveText(dir);

  const uploaded = page.waitForRequest((r) => r.method() === "POST" && r.url().includes("/upload?"));
  await finder.locator('input[type="file"]').setInputFiles({ name: "big.bin", mimeType: "application/octet-stream", buffer: Buffer.alloc(size, 97) });
  await uploaded;
  await expect(finder.getByText("big.bin")).toBeVisible();
  expect(sh(c, "aos", `stat -c %s ~/${dir}/big.bin`).trim()).toBe(String(size));

  await finder.getByText("big.bin").click({ button: "right" });
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download…" }).click();
  const d = await download;
  expect(d.url()).toContain("/files/raw?");
  expect(d.suggestedFilename()).toBe("big.bin");
  expect(statSync(await d.path()).size).toBe(size);

  exec(c, "aos", "rm", "-rf", `/home/aos/${dir}`);
});

test("Finder's column view walks folders and refreshes live when a file appears", async ({ page }) => {
  const c = loadCompose();
  const root = `pw-cols-${Date.now()}`;
  sh(c, "aos", `mkdir -p ~/${root}/inner && printf 'deep\\n' > ~/${root}/inner/first.txt`);

  const finder = await openHome(page);
  await finder.getByTitle("Column view").click();
  const cols = finder.locator(".finder__column");
  await cols.first().getByText(root).click();
  await cols.nth(1).getByText("inner").click();
  await cols.nth(2).getByText("first.txt").click();
  await expect(finder.locator(".finder__colpreview")).toContainText("first.txt");

  // Nothing is clicked: the open folder's listing follows the Machine.
  sh(c, "aos", `printf 'new\\n' > ~/${root}/inner/second.txt`);
  await expect(cols.nth(2).getByText("second.txt")).toBeVisible({ timeout: 10_000 });

  // The view is kept with the window.
  await page.reload();
  await expect(page.locator('.window[aria-label="Finder"]').last().locator(".finder__column")).toHaveCount(3);

  exec(c, "aos", "rm", "-rf", `/home/aos/${root}`);
});

test("Finder protects a file with 🔒 and asks the Agent about it", async ({ page }) => {
  const c = loadCompose();
  const name = `pw-protect-${Date.now()}.txt`;
  sh(c, "aos", `printf 'keep me\\n' > ~/${name}`);

  const finder = await openHome(page);
  const row = finder.locator(".finder__row", { hasText: name });
  await row.click({ button: "right" });
  await page.getByRole("button", { name: "🔒 Protect" }).click();
  await expect(row.getByTitle("Protected")).toBeVisible();

  await row.click({ button: "right" });
  await page.getByRole("button", { name: "Ask Agent…" }).click();
  const prompt = `e2e-ui: summarise ${name}`;
  await page.getByLabel("What should the Agent do?").fill(prompt);
  await page.getByRole("button", { name: "Start Task" }).click();
  await expect(page.locator('.window[aria-label="Agent"] .tasks__title')).toContainText(prompt);

  await page.locator('.window[aria-label="Agent"]').getByTitle("Close").click();
  await row.click({ button: "right" });
  await page.getByRole("button", { name: "Unprotect" }).click();
  await expect(row.getByTitle("Protected")).toHaveCount(0);

  exec(c, "aos", "rm", "-f", `/home/aos/${name}`);
});
