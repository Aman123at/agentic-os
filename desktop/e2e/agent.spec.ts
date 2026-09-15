// The Agent app (PLAN.md §4.3, M4.2): the Task list with its state filter, the
// live step feed with tokens and cost, a Follow-up from the composer, an Approval
// answered inline instead of in the pop-up, the Audit Log filtered to the Task,
// and the Usage view. Driven by the fake provider (ui-agent.json), so no spend.
import type { Page } from "@playwright/test";

import { expect, loadCompose, openApp, sh, startTask, test } from "./harness";

const agent = (p: Page) => p.locator('.window[aria-label="Agent"]');

test("the Agent app runs a Task, a Follow-up and an inline Approval, then shows its audit and usage", async ({ page }) => {
  const c = loadCompose();
  sh(c, "aos", "rm -rf ~/ui-agent && mkdir -p ~/.ssh && printf 'old\\n' > ~/.ssh/ui-agent-key");
  const prompt = `ui-agent: tidy my notes ${Date.now()}`;

  await page.goto("/");
  await startTask(page, prompt);
  const win = agent(page);
  await expect(win).toBeVisible();

  // The feed streams to the end, with the Task's tokens and cost.
  await expect(win.locator(".tasks__feed").getByText("Wrote ~/ui-agent/notes.txt.")).toBeVisible();
  await expect(win.locator(".tasks__state")).toHaveText("Done");
  await expect(win.locator(".tasks__meta")).toContainText(/in · .* out · \$/);

  // The list shows the Task, and the state filter hides it.
  const row = win.locator(".agent__row", { hasText: prompt });
  await expect(row).toBeVisible();
  await win.getByLabel("Filter by state").selectOption("active");
  await expect(row).toHaveCount(0);
  await win.getByLabel("Filter by state").selectOption("finished");
  await expect(row).toBeVisible();

  // A Follow-up needs an Approval; it is answered in the Agent window, not a pop-up.
  await win.getByLabel("Message the Agent").fill("delete the old key too");
  await win.getByRole("button", { name: "Send" }).click();
  const inline = win.getByRole("group", { name: "Approval for this Task" });
  await expect(inline).toBeVisible();
  await expect(inline.getByText("/home/aos/.ssh").first()).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Approval needed" })).toHaveCount(0);
  await inline.getByRole("button", { name: "Allow once" }).click();
  await expect(win.locator(".tasks__feed").getByText("Deleted the old key as well.")).toBeVisible();
  await expect(win.locator(".tasks__state")).toHaveText("Done");
  expect(sh(c, "aos", "test -e ~/.ssh/ui-agent-key && echo there || echo gone").trim()).toBe("gone");

  // The Audit Log, filtered to this Task, has both Tool calls.
  await win.getByRole("button", { name: "Audit Log" }).click();
  await win.getByLabel("Filter by Task").selectOption({ label: prompt });
  await expect(win.locator(".audit__row", { hasText: "write_file" }).first()).toBeVisible();
  await expect(win.locator(".audit__row", { hasText: "delete" }).first()).toBeVisible();

  // Usage: today, 30 days of bars and the Cost Limits in force.
  await win.getByRole("button", { name: "Usage" }).click();
  await expect(win.getByRole("heading", { name: "Today" })).toBeVisible();
  await expect(win.locator(".usage__bar")).toHaveCount(30);
  await expect(win.getByRole("heading", { name: "Cost Limits" })).toBeVisible();
});

test("the Agent app is in the Dock and comes back on the Task it showed", async ({ page }) => {
  const prompt = `e2e-ui: agent restore ${Date.now()}`;
  await page.goto("/");
  await startTask(page, prompt);
  await expect(agent(page).locator(".tasks__title")).toContainText(prompt);

  await page.reload();
  await expect(agent(page).locator(".tasks__title")).toContainText(prompt);

  // The Dock tile focuses the one Agent window rather than opening another.
  await openApp(page, "Agent");
  await expect(agent(page)).toHaveCount(1);
});
