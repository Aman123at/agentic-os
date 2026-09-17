// The Browser app (PLAN.md M5.2): the suite's Machine is built with
// INCLUDE_BROWSER=true, so the Browser is pinned in the Dock. It opens a page a
// Python server serves inside the Machine (no internet needed), follows a link,
// goes back, and refuses a file: address. An Agent uses the same page (M5.3,
// ui-browser.json): its window opens by itself, and a form submit asks first.
import type { Page } from "@playwright/test";

import { clearLayout, dragDivider as drag, expect, loadCompose, openApp, paneWidth as width, settled, sh, startTask, test } from "./harness";

const PORT = 8765;
// A restored layout may hold an earlier Browser window; the newest is ours.
const browserWin = (p: Page) => p.locator('.window[aria-label="Browser"]').last();

// centre reads the colour in the middle of the page canvas.
async function centre(p: Page): Promise<[number, number, number]> {
  return browserWin(p)
    .locator("canvas.browser__page")
    .evaluate((c: HTMLCanvasElement) => {
      const d = c.getContext("2d")!.getImageData(Math.floor(c.width / 2), Math.floor(c.height / 2), 1, 1).data;
      return [d[0], d[1], d[2]] as [number, number, number];
    });
}

// serve writes the pages and starts their server, once.
function serve() {
  // A dark blue page whose whole area is a link to a light green one, and a
  // shop with a search form.
  sh(
    loadCompose(),
    "aos",
    `mkdir -p ~/pw-web && cd ~/pw-web &&
     printf '%s' '<title>PW One</title><body style="margin:0;background:#123488"><a href="/two.html" style="position:fixed;inset:0">next</a></body>' > index.html &&
     printf '%s' '<title>PW Two</title><body style="margin:0;background:#a8f0b0"><p>two</p></body>' > two.html &&
     printf '%s' '<title>PW Shop</title><body><a href="/two.html">Next page</a><form action="/two.html"><input name="q" aria-label="Query"><button>Go</button></form></body>' > shop.html &&
     (curl -fs http://127.0.0.1:${PORT}/ >/dev/null || (setsid nohup python3 -m http.server ${PORT} --bind 127.0.0.1 >/dev/null 2>&1 &)) &&
     for i in $(seq 50); do curl -fs http://127.0.0.1:${PORT}/ >/dev/null && break; sleep 0.1; done`,
  );
}

// A pending Approval is global server state; never leave one behind.
test.afterEach(async ({ page }) => {
  const deny = page.getByRole("dialog", { name: "Approval needed" }).getByRole("button", { name: "Deny" });
  if (await deny.isVisible().catch(() => false)) await deny.click().catch(() => {});
});

test("the Browser is pinned in the Dock and browses a page served inside the Machine", async ({ context, page: p }) => {
  await clearLayout(context);
  serve();

  await p.goto("/");
  await expect(p.locator('.dock__tile[title="Browser"]')).toBeVisible();
  await openApp(p, "Browser");
  const win = browserWin(p);
  const address = win.getByLabel("Address");
  const canvas = win.locator("canvas.browser__page");

  // A bare host:port on localhost becomes an http address, and the page draws.
  await address.fill(`localhost:${PORT}`);
  await address.press("Enter");
  await expect(address).toHaveValue(`http://localhost:${PORT}/`);
  await expect(address).toHaveAttribute("title", "PW One");
  await expect(canvas).toHaveAttribute("data-drawn", "1");
  await expect.poll(async () => (await centre(p))[2], { message: "the blue page is drawn" }).toBeGreaterThan(100);

  // A click on the page follows the link; Back returns.
  await canvas.click();
  await expect(address).toHaveValue(`http://localhost:${PORT}/two.html`);
  await expect(address).toHaveAttribute("title", "PW Two");
  await expect.poll(async () => (await centre(p))[1], { message: "the green page is drawn" }).toBeGreaterThan(200);
  await win.getByRole("button", { name: "Back" }).click();
  await expect(address).toHaveAttribute("title", "PW One");
  await expect(win.getByRole("button", { name: "Forward" })).toBeEnabled();

  // The Machine's files are not web pages.
  await address.fill("file:///etc/passwd");
  await address.press("Enter");
  await expect(win.getByRole("alert")).toContainText("only opens web pages");
  await expect(address).toHaveValue(`http://localhost:${PORT}/`);
});

test("the Browser comes back on its page after a reload", async ({ page: p }) => {
  await p.goto("/");
  await openApp(p, "Browser");
  const address = browserWin(p).getByLabel("Address");
  await address.fill(`localhost:${PORT}/two.html`);
  await address.press("Enter");
  await expect(address).toHaveAttribute("title", "PW Two");

  // A reload attaches to the same page again.
  await p.reload();
  await expect(browserWin(p).getByLabel("Address")).toHaveValue(`http://localhost:${PORT}/two.html`);
});

test("bookmarks are kept with the Browser window, and the sidebar resizes", async ({ page: p }) => {
  await p.goto("/");
  await openApp(p, "Browser");
  const win = browserWin(p);
  const address = win.getByLabel("Address");
  const sidebar = win.getByRole("complementary", { name: "Bookmarks" });
  const divider = win.getByRole("separator", { name: "Resize the bookmarks sidebar" });

  // The sidebar starts open and empty.
  await settled(win);
  await expect(sidebar).toContainText("No bookmarks yet");
  expect(await width(sidebar)).toBe(180);

  // Star the blue page, then browse to the green one.
  await address.fill(`localhost:${PORT}/`);
  await address.press("Enter");
  await expect(address).toHaveAttribute("title", "PW One");
  await win.getByRole("button", { name: "Bookmark this page" }).click();
  await expect(sidebar.getByRole("button", { name: /PW One/ })).toBeVisible();
  await address.fill(`localhost:${PORT}/two.html`);
  await address.press("Enter");
  await expect(address).toHaveAttribute("title", "PW Two");

  // A reload keeps the bookmark, and it takes the page back.
  await p.waitForTimeout(900);
  await p.reload();
  const win2 = browserWin(p);
  await settled(win2);
  await win2.getByRole("complementary", { name: "Bookmarks" }).getByRole("button", { name: /PW One/ }).click();
  await expect(win2.getByLabel("Address")).toHaveValue(`http://localhost:${PORT}/`);
  await expect.poll(async () => (await centre(p))[2], { message: "the blue page is drawn again" }).toBeGreaterThan(100);
  // The page showing is starred, so the star empties it again.
  await expect(win2.getByRole("button", { name: "Remove bookmark" })).toBeVisible();

  // The divider widens the sidebar, and the toolbar button puts it away.
  await drag(p, divider, 60);
  expect(await width(win2.getByRole("complementary", { name: "Bookmarks" }))).toBe(240);
  await win2.getByRole("button", { name: "Hide bookmarks" }).click();
  expect(await width(win2.getByRole("complementary", { name: "Bookmarks" }))).toBe(0);
  await win2.getByRole("button", { name: "Show bookmarks" }).click();

  // Right-click removes it, and the star fills again.
  await win2.getByRole("complementary", { name: "Bookmarks" }).getByRole("button", { name: /PW One/ }).click({ button: "right" });
  // Scoped to the menu: the star carries the same name.
  await p.locator(".menu").getByRole("button", { name: "Remove Bookmark" }).click();
  await expect(win2.getByRole("complementary", { name: "Bookmarks" })).toContainText("No bookmarks yet");
  await expect(win2.getByRole("button", { name: "Bookmark this page" })).toBeVisible();
});

test("an Agent opens the Browser, types, and asks before submitting a form", async ({ context, page: p }) => {
  await clearLayout(context);
  serve();
  await p.goto("/");
  await startTask(p, `ui-browser: search the local shop ${Date.now()}`);
  // The Agent window answers its Task's Approvals inline; close it to see the pop-up.
  await p.locator('.window[aria-label="Agent"]').getByTitle("Close").click();

  // The Browser window opens by itself on the Agent's page.
  const win = browserWin(p);
  await expect(win).toBeVisible();
  const address = win.getByLabel("Address");
  await expect(address).toHaveAttribute("title", "PW Shop");
  await expect(win.locator("canvas.browser__page")).toHaveAttribute("data-drawn", "1");

  // Pressing the form's button sends it, so the Agent asks first, per site.
  const dialog = p.getByRole("dialog", { name: "Approval needed" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(`submits a form on localhost:${PORT}`);
  await expect(dialog).toContainText('Click [3] button "Go"');
  await expect(dialog.getByRole("button", { name: "Allow for this Task" })).toBeVisible();
  // Meanwhile the toolbar says the Agent is using the page.
  const badge = win.getByRole("button", { name: /Agent is browsing/ });
  await expect(badge).toBeVisible();

  await dialog.getByRole("button", { name: "Allow once" }).click();
  // What the Agent typed went with the form.
  await expect(address).toHaveValue(`http://localhost:${PORT}/two.html?q=hello`);
  await expect(address).toHaveAttribute("title", "PW Two");
  // The Task has finished, so the page is the user's again.
  await expect(badge).toBeHidden();
});
