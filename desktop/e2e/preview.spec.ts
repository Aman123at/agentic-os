// Preview (PLAN.md §4.3, M4.3): files opened from the Finder show in their own
// Preview window. An image loads from /files/raw, a PDF renders page by page
// through pdf.js, and a video plays from Range requests. Quick Look shows the
// same preview inside the Finder.
import { expect, exec, loadCompose, openApp, test } from "./harness";
import { pdf, png, putFile, webm } from "./fixtures";

test("Preview shows an image, pages through a PDF and plays a video through Range requests", async ({ page }) => {
  const c = loadCompose();
  const dir = `pw-preview-${Date.now()}`;
  await page.goto("/");
  putFile(c, `~/${dir}/shot.png`, png(64, 48));
  putFile(c, `~/${dir}/doc.pdf`, pdf(["First page", "Second page"]));
  putFile(c, `~/${dir}/clip.webm`, await webm(page));

  await openApp(page, "Finder");
  const finder = page.locator('.window[aria-label="Finder"]').last();
  await finder.locator(".finder__sidebar").getByText("Home").click();
  await finder.getByText(dir).dblclick();

  // An image, at its own size.
  await finder.getByText("shot.png").dblclick();
  const image = page.locator('.window[aria-label="shot.png"]');
  await expect(image.locator("img")).toBeVisible();
  await expect.poll(() => image.locator("img").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(64);

  // Opening the same file again focuses its window rather than opening another.
  // The Preview window covers the Finder's row, so the double-click is sent to it.
  await finder.getByText("shot.png").dispatchEvent("dblclick");
  await expect(page.locator('.window[aria-label="shot.png"]')).toHaveCount(1);
  await image.getByTitle("Close").click();

  // A PDF, one page at a time.
  await finder.getByText("doc.pdf").dblclick();
  const doc = page.locator('.window[aria-label="doc.pdf"]');
  await expect(doc.getByText("Page 1 of 2")).toBeVisible();
  await expect(doc.locator("canvas")).toBeVisible();
  await doc.getByTitle("Next page").click();
  await expect(doc.getByText("Page 2 of 2")).toBeVisible();
  await doc.getByTitle("Close").click();

  // A video, which the browser fetches in ranges.
  const partial = page.waitForResponse((r) => r.url().includes("/files/raw?") && r.url().includes("clip.webm") && r.status() === 206);
  await finder.getByText("clip.webm").dblclick();
  const clip = page.locator('.window[aria-label="clip.webm"]');
  await partial;
  await expect.poll(() => clip.locator("video").evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThanOrEqual(1);
  await clip.getByTitle("Close").click();

  // Quick Look renders through Preview too.
  await finder.getByText("shot.png").click();
  await page.keyboard.press("Space");
  await expect(page.locator(".quicklook img")).toBeVisible();
  await page.keyboard.press("Escape");

  exec(c, "aos", "rm", "-rf", `/home/aos/${dir}`);
});
