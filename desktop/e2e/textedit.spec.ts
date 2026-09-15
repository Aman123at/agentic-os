// TextEdit (PLAN.md §4.3, M4.3): a text file opened from the Finder edits in
// CodeMirror, ⌘S (Ctrl+S) saves it through FileService, and saving over a file
// that changed on disk since it was opened asks first. An Agent's
// open_in_desktop opens a text file straight into TextEdit (ui-open.json).
import type { Page } from "@playwright/test";

import { expect, exec, loadCompose, openApp, sh, startTask, test } from "./harness";

const disk = (path: string) => sh(loadCompose(), "aos", `cat ${path}`);

async function typeAtEnd(page: Page, text: string) {
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type(text);
}

test("TextEdit edits a file, saves with ⌘S and asks before overwriting a change made on disk", async ({ page }) => {
  const c = loadCompose();
  const dir = `pw-textedit-${Date.now()}`;
  const file = `~/${dir}/notes.md`;
  sh(c, "aos", `mkdir -p ~/${dir} && printf 'line one\\n' > ${file}`);

  await page.goto("/");
  await openApp(page, "Finder");
  const finder = page.locator('.window[aria-label="Finder"]').last();
  await finder.locator(".finder__sidebar").getByText("Home").click();
  await finder.getByText(dir).dblclick();
  await finder.getByText("notes.md").dblclick();

  const te = page.locator('.window[aria-label="notes.md"]');
  const editor = te.locator(".cm-content");
  await expect(editor).toContainText("line one");

  await editor.click();
  await typeAtEnd(page, "line two");
  await expect(te.getByText("Edited")).toBeVisible();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(te.getByText("Edited")).toHaveCount(0);
  await expect.poll(() => disk(file)).toBe("line one\nline two");

  // Something else changes the file; saving now asks before replacing it.
  sh(c, "aos", `printf 'changed on disk\\n' > ${file}`);
  await typeAtEnd(page, " and three");
  await page.keyboard.press("ControlOrMeta+s");
  const ask = te.getByRole("alertdialog");
  await expect(ask).toContainText("changed on disk");
  expect(disk(file)).toBe("changed on disk\n");
  await ask.getByRole("button", { name: "Overwrite" }).click();
  await expect.poll(() => disk(file)).toBe("line one\nline two and three");
  await expect(te.getByText("Edited")).toHaveCount(0);

  // Revert takes the version on disk instead.
  sh(c, "aos", `printf 'the machine wins\\n' > ${file}`);
  await editor.click();
  await typeAtEnd(page, "!");
  await page.keyboard.press("ControlOrMeta+s");
  await te.getByRole("alertdialog").getByRole("button", { name: "Revert" }).click();
  await expect(editor).toContainText("the machine wins");
  expect(disk(file)).toBe("the machine wins\n");

  exec(c, "aos", "rm", "-rf", `/home/aos/${dir}`);
});

test("an Agent's open_in_desktop opens a text file in TextEdit", async ({ page }) => {
  const c = loadCompose();
  sh(c, "aos", "rm -rf ~/ui-open");
  await page.goto("/");
  await startTask(page, `ui-open: write a report and show it ${Date.now()}`);
  const te = page.locator('.window[aria-label="report.txt"]');
  await expect(te.locator(".cm-content")).toContainText("The report.");
});
