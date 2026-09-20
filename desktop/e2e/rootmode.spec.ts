// Root Mode switch and Restart AOS (PLAN.md §18 M7.8, M7.9). These drive the
// System pane against a faked aosd: the SystemService RPCs (SetRootMode, Restart
// and the boot_id in Info) are answered with page.route, so the specs cover the
// warning gate, the password (wrong, locked out, right), the running-Agent
// notice, cancel, switch-off and the restart overlay without restarting the
// shared test Machine. The switch and Restart both reload on a new boot_id.
import { create, toBinary, type DescMessage, type MessageInitShape } from "@bufbuild/protobuf";
import { base64Encode } from "@bufbuild/protobuf/wire";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Page, Route } from "@playwright/test";

import {
  RootModeAttemptSchema,
  RootModeBlockedSchema,
  RootModeLockoutSchema,
} from "../src/gen/aos/v1/services_pb";
import { TaskState } from "../src/gen/aos/v1/types_pb";
import { clearLayout, expect, openViaSpotlight, test } from "./harness";

// connectError fulfils a route with a Connect unary JSON error: a non-200 status
// with the code string and base64 proto details the client decodes (verified
// against @connectrpc/connect's error-json parser).
type Detail = { type: string; value: string };

function detail<D extends DescMessage>(schema: D, msg: MessageInitShape<D>): Detail {
  return { type: schema.typeName, value: base64Encode(toBinary(schema, create(schema, msg))) };
}

async function connectError(route: Route, status: number, code: string, message: string, details: Detail[]): Promise<void> {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ code, message, details }) });
}

// systemPane opens System Settings and its System pane (the first one).
async function systemPane(page: Page) {
  await page.goto("/");
  await openViaSpotlight(page, "System Settings");
  const win = page.locator('.window[aria-label="System Settings"]').last();
  await win.locator(".set__navitem", { hasText: "System" }).click();
  await expect(win.locator(".set__title", { hasText: "System" })).toBeVisible();
  return win;
}

// rewriteInfo makes Info return the real body with boot_id (and optionally
// root_mode) overridden, so the overlay's poll sees the change it waits for.
async function rewriteInfo(page: Page, bootId: () => string, rootMode?: () => boolean) {
  await page.route("**/aos.v1.SystemService/Info", async (route) => {
    const resp = await route.fetch();
    const json = (await resp.json()) as Record<string, unknown>;
    json.bootId = bootId();
    if (rootMode) json.rootMode = rootMode();
    await route.fulfill({ response: resp, json });
  });
}

test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("Restart AOS confirms, shows the overlay and reloads on a new boot_id", async ({ page }) => {
  let restarted = false;
  await rewriteInfo(page, () => (restarted ? "boot-after-restart" : "boot-before"));
  await page.route("**/aos.v1.SystemService/Restart", async (route) => {
    restarted = true;
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });

  const win = await systemPane(page);
  await page.evaluate(() => ((window as unknown as { __nav?: string }).__nav = "before"));
  await win.getByRole("button", { name: "Restart AOS" }).click();

  const modal = page.getByRole("dialog", { name: "Restart AOS" });
  await expect(modal).toBeVisible();
  await modal.getByRole("button", { name: "Restart", exact: true }).click();

  await expect(page.getByTestId("rootmode-working")).toBeVisible();
  // The page reloads once boot_id changes: the pre-reload sentinel is gone and
  // the desktop comes back signed in.
  await expect.poll(() => page.evaluate(() => (window as unknown as { __nav?: string }).__nav)).toBeUndefined();
  await expect(page.locator(".desktop")).toBeVisible({ timeout: 20_000 });
});

test("Restart AOS times out and offers Try again", async ({ page }) => {
  // Restart is accepted but nothing ever changes boot_id, so the overlay waits
  // out its (e2e-shortened) timeout and offers the retry and the logs hint.
  await page.route("**/aos.v1.SystemService/Restart", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });
  const win = await systemPane(page);
  await win.getByRole("button", { name: "Restart AOS" }).click();
  await page.getByRole("dialog", { name: "Restart AOS" }).getByRole("button", { name: "Restart", exact: true }).click();

  const overlay = page.getByTestId("rootmode-working");
  await expect(overlay).toBeVisible();
  await expect(overlay.getByRole("button", { name: "Try again" })).toBeVisible({ timeout: 15_000 });
  await expect(overlay).toContainText("sudo aos daemon logs");
});

test("Root Mode: the warning gates Continue and Cancel changes nothing", async ({ page }) => {
  const win = await systemPane(page);
  const toggle = win.getByRole("switch", { name: "Start in Root Mode" });
  await expect(toggle).toHaveAttribute("aria-checked", "false");
  await toggle.click();

  const warn = page.getByRole("dialog", { name: "Turn on Root Mode" });
  await expect(warn).toBeVisible();
  const cont = warn.getByRole("button", { name: "Continue" });
  await expect(cont).toBeDisabled();
  await warn.getByText("I understand these actions cannot be undone").click();
  await expect(cont).toBeEnabled();

  await warn.getByRole("button", { name: "Cancel" }).click();
  await expect(warn).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-checked", "false");
});

test("Root Mode: a wrong password reports the tries left, then a lockout", async ({ page }) => {
  let attempt = 0;
  await page.route("**/aos.v1.SystemService/SetRootMode", async (route) => {
    attempt += 1;
    if (attempt <= 1) {
      await connectError(route, 401, "unauthenticated", "incorrect password", [detail(RootModeAttemptSchema, { attemptsLeft: 3 })]);
    } else {
      await connectError(route, 429, "resource_exhausted", "too many attempts", [
        detail(RootModeLockoutSchema, { until: timestampFromDate(new Date(Date.now() + 15 * 60_000)) }),
      ]);
    }
  });

  const win = await systemPane(page);
  await win.getByRole("switch", { name: "Start in Root Mode" }).click();
  const warn = page.getByRole("dialog", { name: "Turn on Root Mode" });
  await warn.getByText("I understand these actions cannot be undone").click();
  await warn.getByRole("button", { name: "Continue" }).click();

  const pw = page.getByRole("dialog", { name: "Enter your password" });
  await pw.getByLabel("Password").fill("wrong-1");
  await pw.getByRole("button", { name: "Turn on Root Mode" }).click();
  await expect(pw.getByText("Incorrect password — 3 attempts left")).toBeVisible();
  await expect(pw.getByLabel("Password")).toHaveValue("");

  await pw.getByLabel("Password").fill("wrong-2");
  await pw.getByRole("button", { name: "Turn on Root Mode" }).click();
  await expect(pw.getByText(/Too many attempts — try again at/)).toBeVisible();
  await expect(pw.getByLabel("Password")).toBeDisabled();
});

test("Root Mode: the right password switches, overlays and reloads", async ({ page }) => {
  let switched = false;
  await rewriteInfo(page, () => (switched ? "boot-root" : "boot-standard"));
  await page.route("**/aos.v1.SystemService/SetRootMode", async (route) => {
    switched = true;
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });

  const win = await systemPane(page);
  await page.evaluate(() => ((window as unknown as { __nav?: string }).__nav = "before"));
  await win.getByRole("switch", { name: "Start in Root Mode" }).click();
  const warn = page.getByRole("dialog", { name: "Turn on Root Mode" });
  await warn.getByText("I understand these actions cannot be undone").click();
  await warn.getByRole("button", { name: "Continue" }).click();

  const pw = page.getByRole("dialog", { name: "Enter your password" });
  await pw.getByLabel("Password").fill("correct-horse-battery");
  await pw.getByRole("button", { name: "Turn on Root Mode" }).click();

  await expect(page.getByRole("dialog", { name: "Switching to Root Mode…" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __nav?: string }).__nav)).toBeUndefined();
  await expect(page.locator(".desktop")).toBeVisible({ timeout: 20_000 });
});

test("Root Mode: a Task that starts mid-modal is surfaced as the notice", async ({ page }) => {
  await page.route("**/aos.v1.SystemService/SetRootMode", async (route) => {
    await connectError(route, 412, "failed_precondition", "an Agent is still working", [
      detail(RootModeBlockedSchema, { tasks: [{ id: "t_busy", title: "Reindex the archive", state: TaskState.RUNNING }] }),
    ]);
  });

  const win = await systemPane(page);
  await win.getByRole("switch", { name: "Start in Root Mode" }).click();
  const warn = page.getByRole("dialog", { name: "Turn on Root Mode" });
  await warn.getByText("I understand these actions cannot be undone").click();
  await warn.getByRole("button", { name: "Continue" }).click();
  const pw = page.getByRole("dialog", { name: "Enter your password" });
  await pw.getByLabel("Password").fill("whatever");
  await pw.getByRole("button", { name: "Turn on Root Mode" }).click();

  const notice = page.getByTestId("rootmode-blocked");
  await expect(notice).toBeVisible();
  await expect(notice).toContainText("Reindex the archive");
  await expect(notice).toContainText("Running");
});

test("Root Mode: a running Agent springs the switch back with no modal", async ({ page }) => {
  // Seed the boot-time Task list with an active Task, so the live list the switch
  // reads has one and refuses to open the warning at all (M7.9).
  await page.route("**/aos.v1.TaskService/ListTasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ tasks: [{ id: "t_live", title: "Nightly backup", state: "TASK_STATE_RUNNING" }] }),
    });
  });

  const win = await systemPane(page);
  const toggle = win.getByRole("switch", { name: "Start in Root Mode" });
  await toggle.click();

  await expect(page.getByRole("dialog", { name: "Turn on Root Mode" })).toBeHidden();
  const notice = page.getByTestId("rootmode-blocked");
  await expect(notice).toBeVisible();
  await expect(notice).toContainText("Nightly backup");
  await expect(toggle).toHaveAttribute("aria-checked", "false");
});

test("Root Mode: switching off needs one confirm and no password", async ({ page }) => {
  // Report Root Mode as on, so the switch offers to turn it off.
  let off = false;
  await rewriteInfo(page, () => (off ? "boot-standard" : "boot-root"), () => !off);
  await page.route("**/aos.v1.SystemService/SetRootMode", async (route) => {
    off = true;
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });

  const win = await systemPane(page);
  const toggle = win.getByRole("switch", { name: "Start in Root Mode" });
  await expect(toggle).toHaveAttribute("aria-checked", "true");
  await page.evaluate(() => ((window as unknown as { __nav?: string }).__nav = "before"));
  await toggle.click();

  const confirm = page.getByRole("dialog", { name: "Turn off Root Mode" });
  await expect(confirm).toBeVisible();
  // No password field in the off path.
  await expect(confirm.getByLabel("Password")).toHaveCount(0);
  await confirm.getByRole("button", { name: "Turn off Root Mode" }).click();

  await expect(page.getByRole("dialog", { name: "Switching to Standard Mode…" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __nav?: string }).__nav)).toBeUndefined();
  await expect(page.locator(".desktop")).toBeVisible({ timeout: 20_000 });
});

test("Clear Root Mode history: only in Root Mode, confirms, overlays and reloads", async ({ page }) => {
  // Standard Mode has nothing of Root's, so the section is not offered (M7.12).
  const standard = await systemPane(page);
  await expect(standard.getByRole("button", { name: "Clear history" })).toHaveCount(0);

  // Report Root Mode as on; the section appears and the clear runs, flipping
  // boot_id so the overlay's poll sees the restart.
  let cleared = false;
  await rewriteInfo(page, () => (cleared ? "boot-fresh-root" : "boot-root"), () => true);
  await page.route("**/aos.v1.SystemService/ClearRootHistory", async (route) => {
    cleared = true;
    await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });

  const win = await systemPane(page);
  await page.evaluate(() => ((window as unknown as { __nav?: string }).__nav = "before"));
  await win.getByRole("button", { name: "Clear history" }).click();

  const confirm = page.getByRole("dialog", { name: "Clear Root Mode history" });
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText("is real and stays");
  await confirm.getByRole("button", { name: "Clear history" }).click();

  await expect(page.getByRole("dialog", { name: "Clearing Root Mode history…" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __nav?: string }).__nav)).toBeUndefined();
  await expect(page.locator(".desktop")).toBeVisible({ timeout: 20_000 });
});

test("Clear Root Mode history: a running Agent is surfaced as the notice", async ({ page }) => {
  await rewriteInfo(page, () => "boot-root", () => true);
  await page.route("**/aos.v1.SystemService/ClearRootHistory", async (route) => {
    await connectError(route, 412, "failed_precondition", "an Agent is still working", [
      detail(RootModeBlockedSchema, { tasks: [{ id: "t_busy", title: "Reindex the archive", state: TaskState.RUNNING }] }),
    ]);
  });

  const win = await systemPane(page);
  await win.getByRole("button", { name: "Clear history" }).click();
  await page.getByRole("dialog", { name: "Clear Root Mode history" }).getByRole("button", { name: "Clear history" }).click();

  const notice = page.getByTestId("rootmode-blocked");
  await expect(notice).toBeVisible();
  await expect(notice).toContainText("Reindex the archive");
  await expect(notice).toContainText("Running");
});
