// The frame watchdog (PLAN.md §4.3, §22, M4.6): Liquid Glass leans on the
// compositor's backdrop blur, so on a weak GPU it can drop frames. With Glass on,
// the watchdog measures frame times (rAF gaps plus the Long Animation Frames API)
// and, if frames stay over budget for a sustained stretch, switches Glass off and
// posts a notification. Here we turn Glass on, then block the main thread over
// several frames so the meter sees frames well past its 60 ms budget, and assert
// Glass goes back off with the notification. No model, no spend.
import { clearLayout, expect, openViaSpotlight, test } from "./harness";

test.beforeEach(async ({ context }) => {
  await clearLayout(context);
});

test("sustained slow frames switch Liquid Glass off, with a notification", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "System Settings");
  const win = page.locator('.window[aria-label="System Settings"]').last();
  await win.locator(".set__navitem", { hasText: "Appearance" }).click();

  const glass = win.getByRole("switch", { name: "Liquid Glass" });
  await glass.click();
  await expect(page.locator("html")).toHaveAttribute("data-glass", "on");

  // Block the main thread for ~90 ms at a time, yielding between so the watchdog's
  // rAF tick runs and measures each long gap. A dozen bursts clears its
  // six-bad-frames trip threshold well inside the three-second window.
  await page.evaluate(async () => {
    for (let i = 0; i < 12; i++) {
      const t0 = performance.now();
      while (performance.now() - t0 < 90) {
        /* burn a frame past the 60 ms budget */
      }
      await new Promise((r) => setTimeout(r, 30));
    }
  });

  // The watchdog tripped: Glass is off again…
  await expect(page.locator("html")).not.toHaveAttribute("data-glass", "on");

  // …and it said why, in the Notification Center.
  await page.locator(".menubar__bell").click();
  const nc = page.getByRole("dialog", { name: "Notification Center" });
  await expect(nc.locator(".nc__item", { hasText: "Liquid Glass turned off" })).toBeVisible();

  // The switch reflects the off state; leave Glass off for later specs.
  await expect(glass).toHaveAttribute("aria-checked", "false");
});

// Switching the theme has to repaint the menu bar and the Dock. They are
// backdrop-filtered layers, which Chrome does not re-read when only an
// ancestor's custom properties change, so on the Host where M4.8 item 8.9 was
// found they kept the old palette until some unrelated repaint. It does not
// reproduce under headless Chromium — the palette swap alone is enough there —
// so this spec cannot assert the pixels; it guards the nudge that fixes it:
// applyTheme drops the filters for a frame (data-theming) and puts them back.
test("switching the theme nudges the backdrop layers to repaint", async ({ page }) => {
  await page.goto("/");
  await openViaSpotlight(page, "System Settings");
  const win = page.locator('.window[aria-label="System Settings"]').last();
  await win.locator(".set__navitem", { hasText: "Appearance" }).click();
  const themes = win.getByRole("radiogroup", { name: "Theme" });
  const html = page.locator("html");

  const seen = page.waitForFunction(() => document.documentElement.hasAttribute("data-theming"));
  await themes.getByRole("radio", { name: "Dark" }).click();
  await seen;
  await expect(html).toHaveAttribute("data-theme", "dark");
  // …and the filters come back, so Liquid Glass is not left switched off.
  await expect(html).not.toHaveAttribute("data-theming", "");
  await expect(page.locator(".menubar")).toHaveCSS("background-color", "rgba(38, 38, 42, 0.72)");

  // Leave the Desktop on the default for the specs that follow.
  await themes.getByRole("radio", { name: /^Auto/ }).click();
});
