// Approval pop-up (PLAN.md §7, §18, M3.4): a Task that deletes a Protected Path
// raises an Approval; the modal shows the 🔒 path with only Deny / Allow once
// (Protected Paths are never grantable). Allowing lets the Task finish. Driven by
// the fake provider (approval.json), so no model spend.
import { expect, loadCompose, sh, startTask, test } from "./harness";

const KEY = "/home/aos/.ssh/id_ed25519";
const PROTECTED_DIR = "/home/aos/.ssh";

// A pending Approval is global server state; never leave one behind, or its modal
// blocks every later spec. Decide any still-open dialog after the test.
test.afterEach(async ({ page }) => {
  const allow = page
    .getByRole("dialog", { name: "Approval needed" })
    .getByRole("button", { name: "Allow once" });
  if (await allow.isVisible().catch(() => false)) await allow.click().catch(() => {});
});

test("a Protected-Path delete raises an Approval that gates the Task", async ({ page }) => {
  const c = loadCompose();
  sh(c, "aos", `mkdir -p ~/.ssh && printf 'pw-key\\n' > ${KEY} && chmod 600 ${KEY}`);

  await page.goto("/");
  await startTask(page, "e2e-approval: delete my SSH key");

  const dialog = page.getByRole("dialog", { name: "Approval needed" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText("Protected Paths this would change")).toBeVisible();
  // The 🔒 path is the Protected directory the delete would change (it appears in
  // both the reasons and the Protected-Paths list, so match the first).
  await expect(dialog.getByText(PROTECTED_DIR).first()).toBeVisible();
  // Protected Paths can be allowed once but not granted for the whole Task.
  await expect(dialog.getByRole("button", { name: "Allow for this Task" })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Deny" })).toBeVisible();

  await dialog.getByRole("button", { name: "Allow once" }).click();
  await expect(dialog).toBeHidden();

  // With the delete allowed, the Task runs to completion.
  await expect(page.locator(".tasks__state")).toHaveText("Done");
});
