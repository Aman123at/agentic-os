// The Desktop's authentication screens (PLAN.md §18 M6.5), driven against the
// fake aosd in fake.ts — no Docker, no real backend. Covers the three boot
// phases (sign in, forced first change, resume), the expiry modal, and logout
// closing the running desktop.
import { expect, test } from "@playwright/test";

import { installFakeBackend, seedSession } from "./fake";

const PASSWORD = "correct-horse-battery";

test("a wrong password is refused and the login screen stays", async ({ page }) => {
  await installFakeBackend(page, { password: PASSWORD, mustChange: false });
  await page.goto("/");

  await expect(page.getByText("Sign in to the Desktop")).toBeVisible();
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("wrong-password");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByRole("alert")).toContainText("incorrect");
  await expect(page.locator(".menubar")).toHaveCount(0);
});

test("a good password signs in and loads the shell", async ({ page }) => {
  await installFakeBackend(page, { password: PASSWORD, mustChange: false });
  await page.goto("/");

  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.locator(".menubar")).toBeVisible();
});

test("a system-generated password forces a change before the shell", async ({ page }) => {
  await installFakeBackend(page, { password: PASSWORD, mustChange: true });
  await page.goto("/");

  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();

  // The forced-change phase, not the shell.
  await expect(page.getByText("Choose a password")).toBeVisible();
  await expect(page.locator(".menubar")).toHaveCount(0);

  // Too short is refused, not merely warned.
  await page.getByLabel("New password").fill("short");
  await page.getByLabel("Confirm password").fill("short");
  await expect(page.getByRole("button", { name: "Set password" })).toBeDisabled();

  await page.getByLabel("New password").fill("a-long-enough-password");
  await page.getByLabel("Confirm password").fill("a-long-enough-password");
  await page.getByRole("button", { name: "Set password" }).click();

  await expect(page.locator(".menubar")).toBeVisible();
});

test("a stored session resumes without asking for the password", async ({ page }) => {
  const { calls } = await installFakeBackend(page, { password: PASSWORD, mustChange: false });
  await seedSession(page);
  await page.goto("/");

  // Straight to the shell; the login screen never appears.
  await expect(page.locator(".menubar")).toBeVisible();
  await expect(page.getByText("Sign in to the Desktop")).toHaveCount(0);
  expect(calls.refresh).toBeGreaterThan(0);
});

test("an expired session shows the modal and re-auth resumes in place", async ({ page }) => {
  await installFakeBackend(page, { password: PASSWORD, mustChange: false, streamUnauthorizedTimes: 1 });
  await page.goto("/");

  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();

  // The event stream's 401 raises the modal over the (still-mounted) desktop.
  const modal = page.getByRole("dialog", { name: "Session expired" });
  await expect(modal).toBeVisible();
  await expect(page.locator(".desktop")).toBeVisible();

  await modal.getByLabel("Username").fill("admin");
  await modal.getByLabel("Password").fill(PASSWORD);
  await modal.getByRole("button", { name: "Resume" }).click();

  // Re-auth resumes without a reload: the modal goes and the desktop stays.
  await expect(modal).toHaveCount(0);
  await expect(page.locator(".menubar")).toBeVisible();
});

test("logout closes the desktop and returns to the login screen", async ({ page }) => {
  const { calls } = await installFakeBackend(page, { password: PASSWORD, mustChange: false });
  await page.goto("/");

  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator(".menubar")).toBeVisible();

  const streamsBefore = calls.subscribe;
  await page.locator(".menubar__logo").click();
  await page.getByText("Log Out…").click();

  await expect(page.getByText("Sign in to the Desktop")).toBeVisible();
  await expect(page.locator(".menubar")).toHaveCount(0);
  expect(calls.signOut).toBeGreaterThan(0);
  // The event stream stopped: no new Subscribe once the shell is gone.
  await page.waitForTimeout(1000);
  expect(calls.subscribe).toBe(streamsBefore);
});
