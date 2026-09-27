// Code's speed (PLAN.md §16, M8.14) on a generated Workspace of 10 000 files,
// against the fake aosd in fake.ts: a Code window is interactive within
// 400 ms once its code is loaded, ⌘P shows results within 100 ms p95, and F12
// lands within 150 ms p95. Each is timed in the page, from the input event to
// the animation frame that shows the result. aosd's own side of ⌘P and F12 is
// timed by internal/code/perf_test.go.
import { expect, test, type Page } from "@playwright/test";

import { installFakeAosd } from "./fake";

const ROOT = "/home/aos/big";
const FILES = 10_000;
const TARGET = { open: 400, quickOpen: 100, definition: 150 };

const WORDS = (
  "app api auth button cache client config core data editor event file form handler http index input layout list loader logger main menu " +
  "model modal node page panel parser query router schema search server service session settings store table task theme token tree user " +
  "util view widget window worker"
).split(" ");
const EXTS = [".ts", ".tsx", ".go", ".py", ".css", ".json", ".md"];
const cap = (w: string) => w[0].toUpperCase() + w.slice(1);

// The i-th generated file: packages of source folders, a mix of languages,
// every name unique; the last 2 000 in one folder. Go files define F<i>.
function fileName(i: number): string {
  const w = (k: number) => WORDS[(i * 7 + k * 13) % WORDS.length];
  return `${w(4)}${cap(w(5))}${i}${EXTS[i % EXTS.length]}`;
}
const FLAT = 2_000;
function filePath(i: number): string {
  const w = (k: number) => WORDS[(i * 7 + k * 13) % WORDS.length];
  if (i >= FILES - FLAT) return `${ROOT}/generated/${fileName(i)}`;
  return `${ROOT}/packages/${w(1)}-${i % 40}/src/${w(2)}/${w(3)}/${fileName(i)}`;
}

// F12's targets: Go files spread across the tree, each called from main.go.
const GO_TARGETS = Array.from({ length: 21 }, (_, k) => 2 + 7 * (k * 67));

function bigWorkspace(): Record<string, string> {
  const files: Record<string, string> = {};
  for (let i = 0; i < FILES; i++) files[filePath(i)] = i % EXTS.length === 2 ? `package p\n\nfunc F${i}() {}\n` : "";
  files[`${ROOT}/cmd/main.go`] = `package main\n\nfunc main() {\n${GO_TARGETS.map((i) => `\tF${i}()\n`).join("")}}\n`;
  return files;
}

const p95 = (ms: number[]) => [...ms].sort((a, b) => a - b)[Math.ceil(ms.length * 0.95) - 1];

async function boot(page: Page) {
  // The time of each key and pointer press, as the page saw it.
  await page.addInitScript(() => {
    for (const type of ["keydown", "pointerdown"]) addEventListener(type, (e) => ((window as unknown as { __input: number }).__input = e.timeStamp), true);
  });
  await installFakeAosd(page, { files: bigWorkspace() });
  await page.goto("/");
  await expect(page.locator(".menubar")).toBeVisible();
}

// settled waits for done(arg) to hold and returns how long after the last
// input it first did, to the animation frame.
async function settled<A>(page: Page, done: (arg: A) => boolean, arg: A): Promise<number> {
  const src = done.toString();
  const took = await page.waitForFunction(
    ([s, a]) => {
      const ok = new Function("a", `return (${s})(a)`)(a) as boolean;
      return ok && Math.max(0.001, performance.now() - (window as unknown as { __input: number }).__input);
    },
    [src, arg] as const,
    { polling: "raf", timeout: 5_000 },
  );
  return (await took.jsonValue()) as number;
}

function report(what: string, ms: number[], target: number) {
  const took = p95(ms);
  const sorted = [...ms].sort((a, b) => a - b);
  console.log(`    ${what}: p95 ${took.toFixed(0)} ms, median ${sorted[sorted.length >> 1].toFixed(0)} ms, max ${sorted.at(-1)!.toFixed(0)} ms over ${ms.length} (target < ${target} ms)`);
  return took;
}

async function openBig(page: Page) {
  await page.locator('.dock__tile[title="Code"]').click();
  const asking = page.locator('.window[aria-label="Code"]');
  await asking.getByLabel("Folder to open").fill("~/big");
  await asking.getByRole("button", { name: "Open" }).click();
  // Interactive: the Explorer lists the folder.
  return settled(
    page,
    () => [...document.querySelectorAll('.window[aria-label="big"] [role="treeitem"]')].some((r) => r.textContent?.includes("packages")),
    null,
  );
}

test("a Code window on a 10 000-file folder is interactive within 400 ms once loaded", async ({ page }) => {
  await boot(page);
  const took: number[] = [];
  for (let i = 0; i < 6; i++) {
    const ms = await openBig(page);
    const win = page.locator('.window[aria-label="big"]');
    await expect(win.getByRole("treeitem", { name: /packages/ })).toBeVisible();
    if (i > 0) took.push(ms); // the first loads Code's modules
    await win.locator(".traffic__close").click();
    await expect(win).toHaveCount(0);
  }
  expect(report("Code window interactive, warm", took, TARGET.open)).toBeLessThan(TARGET.open);
});

test("⌘P shows results within 100 ms p95 on a 10 000-file Workspace", async ({ page }) => {
  await boot(page);
  await openBig(page);
  const win = page.locator('.window[aria-label="big"]');
  await win.getByText("Open a file from the Explorer.").click();
  const palette = win.getByRole("dialog", { name: "Quick Open" });

  // Each key of a file's name, timed until the list shows what is typed now:
  // the first result's matched letters, which are in bold, spell it.
  const took: number[] = [];
  for (let k = 0; k < 8; k++) {
    const name = fileName((k * 1231 + 13) % FILES);
    const query = name.slice(0, name.lastIndexOf("."));
    await page.keyboard.press("ControlOrMeta+p");
    await expect(palette).toBeVisible();
    for (let n = 1; n <= query.length; n++) {
      await page.keyboard.type(query[n - 1]);
      const ms = await settled(
        page,
        (typed) => {
          const first = document.querySelector(".code-palette__item .code-palette__name");
          return [...(first?.querySelectorAll("b") ?? [])].map((b) => b.textContent).join("").toLowerCase() === typed;
        },
        query.slice(0, n).toLowerCase(),
      );
      if (k > 0) took.push(ms); // the first file's keys warm up
    }
    await expect(palette.getByRole("option").filter({ hasText: name }).first()).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(palette).toHaveCount(0);
    await win.getByText("Open a file from the Explorer.").click();
  }
  expect(report("⌘P results, per key", took, TARGET.quickOpen)).toBeLessThan(TARGET.quickOpen);
});

test("F12 lands on a definition in another file within 150 ms p95 on a 10 000-file Workspace", async ({ page }) => {
  await boot(page);
  await openBig(page);
  const win = page.locator('.window[aria-label="big"]');
  const status = win.getByRole("status", { name: "Status bar" });
  await win.getByRole("treeitem", { name: /cmd/ }).click();
  await win.getByRole("treeitem", { name: /main\.go$/ }).click();
  await expect(win.getByRole("tab", { name: /main\.go/, selected: true })).toBeVisible();
  await win.locator(".cm-content").click();

  const took: number[] = [];
  for (const [k, i] of GO_TARGETS.entries()) {
    // The cursor on F<i> in main.go, then F12.
    await page.keyboard.press("Control+g");
    await page.keyboard.type(String(4 + k));
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.activeElement?.classList.contains("cm-content"));
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("ArrowRight");
    await expect(status).toContainText(`Ln ${4 + k}, Col 3`);
    await page.keyboard.press("F12");
    const ms = await settled(
      page,
      (want) => {
        const win = document.querySelector('.window[aria-label="big"]');
        const tab = win?.querySelector('[aria-label="Open files"] [role="tab"][aria-selected="true"]')?.textContent ?? "";
        const bar = win?.querySelector('[role="status"][aria-label="Status bar"]')?.textContent ?? "";
        return tab.includes(want) && bar.includes("Ln 3,"); // func F<i> is line 3
      },
      fileName(i),
    );
    if (k > 0) took.push(ms); // the first F12 builds the index
    // Back to main.go for the next.
    await page.keyboard.press("Control+Minus");
    await expect(win.getByRole("tab", { name: /main\.go/, selected: true })).toBeVisible();
  }
  expect(report("F12 to another file", took, TARGET.definition)).toBeLessThan(TARGET.definition);
});

test("the Explorer and Find in Files draw only the rows in view of thousands", async ({ page }) => {
  await boot(page);
  await openBig(page);
  const win = page.locator('.window[aria-label="big"]');

  // A folder of 2 000 files.
  await win.getByRole("treeitem", { name: /generated/ }).click();
  await expect(win.getByRole("treeitem", { name: /\.(tsx?|go|py|css|json|md)$/ }).first()).toBeVisible();
  const rows = await win.getByRole("treeitem").count();
  expect(rows).toBeGreaterThan(10);
  expect(rows).toBeLessThan(100);

  // A search that matches in every Go file: some 1 400 files.
  await win.getByText("Open a file from the Explorer.").click();
  await page.keyboard.press("ControlOrMeta+Shift+f");
  await page.keyboard.type("package p");
  await expect(win.getByRole("status", { name: "Search results summary" })).toHaveText(/^1,?4\d\d results in 1,?4\d\d files$/);
  const hits = await win.getByRole("treeitem").count();
  expect(hits).toBeGreaterThan(10);
  expect(hits).toBeLessThan(150);
});
