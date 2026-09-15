// Two tabs stay in sync (PLAN.md §18, M3.1): both subscribe to aosd's event
// stream, so a Task started in one tab appears in the other's Notification
// Center. Uses the zero-side-effect ui-hello cassette (no model spend).
import { clearLayout, expect, startTask, test } from "./harness";

// Both tabs start from an empty desktop, so an Agent window an earlier spec left
// in the shared layout can't also list the Task and make the match ambiguous.
test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("a Task started in one tab shows in the other's Notification Center", async ({ page, context }) => {
  const other = await context.newPage();
  await other.goto("/");
  await expect(other.locator(".menubar")).toBeVisible();

  await page.goto("/");
  const prompt = `e2e-ui: say hello ${Date.now()}`;
  await startTask(page, prompt);

  // The other tab, which never created the Task, learns about it over the stream.
  await other.locator(".menubar__bell").click();
  await expect(other.getByRole("dialog", { name: "Notification Center" })).toBeVisible();
  await expect(other.getByText(prompt)).toBeVisible();
});
