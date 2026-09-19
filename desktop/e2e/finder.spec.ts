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
  for (const place of ["Home", "Downloads", "Filesystem", "Trash"]) {
    await expect(sidebar.getByText(place)).toBeVisible();
  }

  // Home lists the file we just created.
  await sidebar.getByText("Home").click();
  await expect(finder.getByText(name)).toBeVisible();

  // Filesystem opens the machine root, now that "/" is reachable (M6.14).
  await sidebar.getByText("Filesystem").click();
  await expect(finder.getByText("home", { exact: true })).toBeVisible();

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

test("Finder windows in two tabs leave connections for the rest of the Desktop", async ({ page, context }) => {
  // Browsers allow six HTTP/1.1 connections to aosd across tabs. Each tab's
  // event stream holds one; watched folders must not take the rest.
  const openFinders = async (p: Page) => {
    await p.goto("/");
    for (const place of ["Home", "Downloads", "Filesystem"]) {
      await openApp(p, "Finder");
      await p.locator('.window[aria-label="Finder"]').last().locator(".finder__sidebar").getByText(place).click();
    }
  };
  await openFinders(page);
  const other = await context.newPage();
  await openFinders(other);

  await page.reload({ timeout: 10_000 });
  await expect(page.locator(".menubar")).toBeVisible();
  await openApp(page, "Finder");
  await expect(page.locator('.window[aria-label="Finder"]').last().locator(".finder__row").first()).toBeVisible({ timeout: 10_000 });
  await other.close();
});

test("the lock badge covers built-in Protected Paths and what is inside them", async ({ page }) => {
  const c = loadCompose();
  const dir = `pw-locked-${Date.now()}`;
  sh(c, "aos", `mkdir -p ~/${dir} && printf 'inside\\n' > ~/${dir}/inside.txt && aos protect ~/${dir}`);

  const finder = await openHome(page);

  // The folder the user locked: badged, and offering to unlock it.
  const folder = finder.locator(".finder__row", { hasText: dir });
  await expect(folder.getByTitle("Protected")).toBeVisible();

  // A file inside a locked folder is protected too, and says so rather than
  // offering a Protect that would change nothing (M4.8 8.3).
  await folder.dblclick();
  const inside = finder.locator(".finder__row", { hasText: "inside.txt" });
  await expect(inside.getByTitle("Protected")).toBeVisible();
  await inside.click({ button: "right" });
  const inherited = page.getByRole("button", { name: "🔒 Protected", exact: true });
  await expect(inherited).toBeVisible();
  await expect(inherited).toBeDisabled();
  await page.keyboard.press("Escape");

  // /shared is a built-in Protected Path, so what is in it is badged too,
  // without anyone having locked anything. It is reached through Filesystem now
  // that "/" is a Place (M6.14) — the Shared sidebar shortcut is gone.
  const guest = `pw-shared-${Date.now()}.txt`;
  sh(c, "aos", `printf 'shared\n' > /shared/${guest}`);
  await finder.locator(".finder__sidebar").getByText("Filesystem").click();
  await finder.locator(".finder__row", { hasText: "shared" }).dblclick();
  const sharedRow = finder.locator(".finder__row", { hasText: guest });
  await expect(sharedRow.getByTitle("Protected")).toBeVisible();
  sh(c, "aos", `rm -f /shared/${guest}`);

  sh(c, "aos", `aos unprotect ~/${dir} >/dev/null 2>&1 || true; rm -rf ~/${dir}`);
});

// A context menu opened near the bottom of the window used to render below its
// edge, where nothing in it could be clicked (PLAN.md M4.8 item 8.7). It now
// flips above the pointer and stays inside the viewport.
test("a context menu near the bottom of the screen stays on screen", async ({ page }) => {
  const c = loadCompose();
  const dir = `pw-menu-${Date.now()}`;
  sh(c, "aos", `mkdir -p ~/${dir} && for i in $(seq 1 60); do printf 'x\\n' > ~/${dir}/file-$i.txt; done`);

  const finder = await openHome(page);
  await finder.locator(".finder__row", { hasText: dir }).dblclick();

  // Put the window's bottom near the bottom of the viewport, then open the menu
  // on the last row that is visible in it.
  const box = (await finder.boundingBox())!;
  const rows = finder.locator(".finder__row");
  const last = rows.nth((await rows.count()) - 1);
  await last.scrollIntoViewIfNeeded();
  await last.click({ button: "right" });

  const menu = page.locator(".menu");
  await expect(menu).toBeVisible();
  const m = (await menu.boundingBox())!;
  const viewport = page.viewportSize()!;
  expect(Math.round(m.y + m.height), "the menu runs past the bottom of the screen").toBeLessThanOrEqual(viewport.height);
  expect(Math.round(m.x + m.width), "the menu runs past the right of the screen").toBeLessThanOrEqual(viewport.width);
  expect(Math.round(m.y), "the menu starts above the top of the screen").toBeGreaterThanOrEqual(0);

  // Every item in it is actually reachable: Move to Trash is the last one.
  await expect(menu.getByRole("button", { name: "Move to Trash" })).toBeVisible();
  expect(box.width).toBeGreaterThan(0);

  await page.keyboard.press("Escape");
  sh(c, "aos", `rm -rf ~/${dir}`);
});

// Every column follows its folder, not just the one Finder is watching
// (PLAN.md M4.8 item 8.17).
test("column view shows a file created behind its back", async ({ page }) => {
  const c = loadCompose();
  const dir = `pw-col-${Date.now()}`;
  sh(c, "aos", `mkdir -p ~/${dir}/sub && printf 'one\\n' > ~/${dir}/sub/one.txt`);

  const finder = await openHome(page);
  await finder.locator(".finder__row", { hasText: dir }).dblclick();
  await finder.getByTitle("Column view").click();

  // Open the subfolder, so the folder above it is a column that is not watched.
  await finder.locator(".finder__colrow", { hasText: "sub" }).click();
  await expect(finder.locator(".finder__colrow", { hasText: "one.txt" })).toBeVisible();

  // A file created in the folder above appears without re-navigating.
  const late = `late-${Date.now()}.txt`;
  sh(c, "aos", `printf 'late\\n' > ~/${dir}/${late}`);
  await expect(finder.locator(".finder__colrow", { hasText: late })).toBeVisible({ timeout: 15_000 });

  sh(c, "aos", `rm -rf ~/${dir}`);
});
