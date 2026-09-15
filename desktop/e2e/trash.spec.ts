// The Trash (PLAN.md §4.3, M4.3): the Dock's Trash opens the Trash app, the
// Finder's Trash view in a window of its own. A file moved to the Trash from the
// Finder shows there, Put Back restores it, and emptying asks first.
import { expect, loadCompose, openApp, sh, test } from "./harness";

test("the Trash in the Dock puts a file back and empties after asking", async ({ page }) => {
  const c = loadCompose();
  const stamp = Date.now();
  const keep = `pw-trash-keep-${stamp}.txt`;
  const drop = `pw-trash-drop-${stamp}.txt`;
  sh(c, "aos", `printf 'keep\\n' > ~/${keep} && printf 'drop\\n' > ~/${drop}`);
  const exists = (name: string) => sh(c, "aos", `test -e ~/${name} && echo yes || echo no`).trim();

  await page.goto("/");
  await openApp(page, "Finder");
  const finder = page.locator('.window[aria-label="Finder"]').last();
  await finder.locator(".finder__sidebar").getByText("Home").click();
  for (const name of [keep, drop]) {
    await finder.locator(".finder__row", { hasText: name }).click({ button: "right" });
    await page.getByRole("button", { name: "Move to Trash" }).click();
    await expect(finder.locator(".finder__row", { hasText: name })).toHaveCount(0);
  }

  await openApp(page, "Trash");
  const trash = page.locator('.window[aria-label="Trash"]');
  await expect(trash.locator(".finder__sidebar")).toHaveCount(0);
  const kept = trash.locator(".finder__row", { hasText: keep });
  await kept.click({ button: "right" });
  await page.getByRole("button", { name: "Put Back" }).click();
  await expect(kept).toHaveCount(0);
  expect(exists(keep)).toBe("yes");

  // Emptying asks first; cancelling keeps everything.
  await expect(trash.locator(".finder__row", { hasText: drop })).toBeVisible();
  await trash.getByRole("button", { name: "Empty" }).click();
  const ask = trash.getByRole("alertdialog");
  await expect(ask).toContainText("permanently");
  await ask.getByRole("button", { name: "Cancel" }).click();
  await expect(trash.locator(".finder__row", { hasText: drop })).toBeVisible();
  await trash.getByRole("button", { name: "Empty" }).click();
  await trash.getByRole("alertdialog").getByRole("button", { name: "Empty Trash" }).click();
  await expect(trash.getByText("The Trash is empty.")).toBeVisible();
  expect(exists(drop)).toBe("no");

  sh(c, "aos", `rm -f ~/${keep}`);
});
