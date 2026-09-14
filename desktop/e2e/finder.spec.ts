// Finder (PLAN.md §4.3, §18, M3.2): the Places sidebar and a real listing read
// over FileService — a file created in the Machine shows up in Home.
import { expect, exec, loadCompose, openApp, sh, test } from "./harness";

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
