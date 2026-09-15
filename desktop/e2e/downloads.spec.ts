// The Downloads stack (PLAN.md §4.3, M4.3): a Dock stack that fans out the newest
// files in ~/Downloads. An Agent downloads aosd's own /healthz (ui-downloads.json),
// so no network is needed; the file shows in the fan and opens from it.
import { expect, loadCompose, sh, startTask, test } from "./harness";

test("the Downloads stack shows a file an Agent downloaded and opens it", async ({ page }) => {
  const c = loadCompose();
  const name = "ui-downloads-health.txt";
  sh(c, "aos", `rm -f ~/Downloads/${name}`);

  await page.goto("/");
  await startTask(page, `ui-downloads: fetch the health check ${Date.now()}`);
  const agent = page.locator('.window[aria-label="Agent"]');
  await expect(agent.locator(".tasks__feed").getByText("Saved the health check")).toBeVisible();
  await expect(agent.locator(".tasks__state")).toHaveText("Done");
  await agent.getByTitle("Close").click();

  await page.locator('.dock__tile[title="Downloads"]').click();
  const fan = page.getByRole("dialog", { name: "Downloads" });
  await fan.getByRole("button", { name: new RegExp(name) }).click();
  await expect(fan).toBeHidden();
  await expect(page.locator(`.window[aria-label="${name}"] .cm-content`)).toContainText("ok");

  // The fan's folder button opens ~/Downloads in the Finder.
  await page.locator('.dock__tile[title="Downloads"]').click();
  await page.getByRole("dialog", { name: "Downloads" }).getByRole("button", { name: "Open in Finder" }).click();
  await expect(page.locator('.window[aria-label="Finder"]').last().locator(".finder__crumb--on")).toHaveText("Downloads");
});
