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

test("the Agent app starts a Task from its toolbar and deletes it from the row menu", async ({ page }) => {
  await page.goto("/");
  await openApp(page, "Agent");
  const win = agent(page);
  const prompt = `e2e-ui: composer task ${Date.now()}`;

  // The New Task composer starts a Task without leaving the app, the same as the
  // CLI, and lands in its live feed.
  await win.getByRole("button", { name: /New Task/ }).click();
  const dialog = page.getByRole("dialog", { name: "New Task" });
  await dialog.getByLabel("What should the Agent do?").fill(prompt);
  await dialog.getByRole("button", { name: "Start Task" }).click();
  await expect(win.locator(".tasks__title")).toContainText(prompt);
  await expect(win.locator(".tasks__feed").getByText("Hello from the Agent. Nothing to do here.")).toBeVisible();
  await expect(win.locator(".tasks__state")).toHaveText("Done");

  // The row's context menu deletes it, behind a confirmation that names it.
  const row = win.locator(".agent__row", { hasText: prompt });
  await expect(row).toBeVisible();
  await row.click({ button: "right" });
  await page.getByRole("button", { name: "Delete Task…" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Delete Task?" });
  await expect(confirm).toBeVisible();
  await confirm.getByRole("button", { name: "Delete" }).click();
  await expect(row).toHaveCount(0);
});

test("the Agent renders its Markdown reply once, with no duplicate summary", async ({ page }) => {
  await page.goto("/");
  await startTask(page, `e2e-md: show the machine status ${Date.now()}`);
  const win = agent(page);
  const feed = win.locator(".tasks__feed");
  await expect(win.locator(".tasks__state")).toHaveText("Done");

  // The reply is parsed as Markdown, not shown with its raw markers: the heading
  // line is bold, the bullets are a real list, and no ** or ` leaks through.
  await expect(feed.locator("strong", { hasText: "Machine status:" })).toBeVisible();
  await expect(feed.locator(".md__list li")).toHaveCount(3);
  await expect(feed.locator("code", { hasText: "1.0 GiB total" })).toBeVisible();
  await expect(feed).not.toContainText("**");

  // The final message is the last feed step; it must not be repeated in a
  // summary block below the feed (the bug this fixes).
  await expect(win.locator(".tasks__summary")).toHaveCount(0);
  await expect(feed.getByText("Total RAM", { exact: false })).toHaveCount(1);
});

test("the Audit Log view clears to a floor and shows everything again", async ({ page }) => {
  await page.goto("/");
  await startTask(page, `e2e-ui: audit clear ${Date.now()}`);
  const win = agent(page);

  // Starting a Task records at least a create_task entry, so the view has rows.
  await win.getByRole("button", { name: "Audit Log" }).click();
  await expect(win.locator(".audit__row").first()).toBeVisible();

  // Clear hides what is in view (a floor, not a delete); Show all brings it back.
  // Exact: this Task's prompt says "clear" too, and a row carrying it would
  // otherwise match the toolbar's button by name.
  await win.getByRole("button", { name: "Clear", exact: true }).click();
  const confirm = page.getByRole("alertdialog", { name: "Clear the Audit Log view?" });
  await expect(confirm).toBeVisible();
  await confirm.getByRole("button", { name: "Clear" }).click();
  await expect(win.locator(".audit__row")).toHaveCount(0);
  await expect(win.getByRole("button", { name: "Show all" })).toBeVisible();

  await win.getByRole("button", { name: "Show all" }).click();
  await expect(win.locator(".audit__row").first()).toBeVisible();
});
