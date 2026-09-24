// Code (PLAN.md M8.3): open a folder, edit, save, Auto Save, reload on an
// outside change, ask before closing with unsaved files — against the fake
// aosd in fake.ts. TextEdit, which now runs on the shared editor module, is
// checked here too.
import { expect, test, type Locator, type Page } from "@playwright/test";

import { installFakeAosd, type FakeMachine, type FakeOptions } from "./fake";

const PROJECT = {
  "/home/aos/project/main.go": "package main\n\nfunc main() {}\n",
  "/home/aos/project/src/util.ts": "export const x = 1;\n",
  "/home/aos/project/README.md": "# Project\n",
  "/home/aos/notes.md": "line one\n",
};

// aosd is the fake Machine, reached from the spec.
async function aosd<T>(page: Page, fn: (m: FakeMachine, arg: string[]) => T, ...arg: string[]): Promise<T> {
  return page.evaluate(([f, a]) => new Function("m", "a", `return (${f})(m, a)`)((window as unknown as { __aosd: FakeMachine }).__aosd, a), [fn.toString(), arg] as const);
}
const disk = (page: Page, path: string) => aosd(page, (m, [p]) => m.read(p), path);

async function boot(page: Page, opts: FakeOptions = {}) {
  await installFakeAosd(page, { files: PROJECT, ...opts });
  await page.goto("/");
  await expect(page.locator(".menubar")).toBeVisible();
}

// openProject opens ~/project in Code from the Dock, as a person would.
async function openProject(page: Page): Promise<Locator> {
  await page.locator('.dock__tile[title="Code"]').click();
  const asking = page.locator('.window[aria-label="Code"]');
  await asking.getByLabel("Folder to open").fill("~/project");
  await asking.getByRole("button", { name: "Open" }).click();
  const win = page.locator('.window[aria-label="project"]');
  await expect(win.getByRole("treeitem", { name: /main\.go/ })).toBeVisible();
  return win;
}

async function openFile(win: Locator, name: string) {
  await win.getByRole("treeitem", { name: new RegExp(`^\\S*\\s*${name.replace(".", "\\.")}$`) }).click();
  await expect(win.getByRole("tab", { name: new RegExp(name.replace(".", "\\.")), selected: true })).toBeVisible();
}

async function typeAtEnd(page: Page, win: Locator, text: string) {
  await win.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type(text);
}

const autoSaveOff = async (win: Locator) => {
  await win.getByRole("button", { name: "Auto Save: On" }).click();
  await expect(win.getByRole("button", { name: "Auto Save: Off" })).toBeVisible();
};

test("Code opens a folder from the Dock, and ⌘S saves an edit through aosd", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Standard Mode · aos");
  await autoSaveOff(win);

  await openFile(win, "main.go");
  await expect(win.locator(".cm-content")).toContainText("func main() {}");
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Go");
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Tab Size: 4");

  await typeAtEnd(page, win, "// edited");
  await expect(win.getByRole("button", { name: "main.go has unsaved changes; close" })).toBeVisible();
  // aosd hears the window's unsaved files, for the Realm switch.
  await expect.poll(() => aosd(page, (m) => Object.values(m.dirty).flat())).toEqual(["/home/aos/project/main.go"]);

  await page.keyboard.press("ControlOrMeta+s");
  await expect.poll(() => disk(page, "/home/aos/project/main.go")).toBe("package main\n\nfunc main() {}\n// edited");
  await expect(win.getByRole("button", { name: "Close main.go" })).toBeVisible();
  await expect.poll(() => aosd(page, (m) => Object.values(m.dirty).flat())).toEqual([]);

  // Folders open lazily, and a second file gets its own tab.
  await win.getByRole("treeitem", { name: /src/ }).click();
  await openFile(win, "util.ts");
  await expect(win.locator(".cm-content")).toContainText("export const x = 1;");
  await expect(win.getByRole("tab")).toHaveCount(2);
});

test("Auto Save saves a second after typing stops, and is on in Standard Mode", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await expect(win.getByRole("button", { name: "Auto Save: On" })).toBeVisible();
  await openFile(win, "README.md");
  await typeAtEnd(page, win, "More.");
  await expect.poll(() => disk(page, "/home/aos/project/README.md"), { timeout: 5000 }).toBe("# Project\nMore.");
  await expect(win.getByRole("button", { name: "Close README.md" })).toBeVisible();
});

test("a clean tab takes a change made on disk; an unsaved one asks", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await autoSaveOff(win);
  await openFile(win, "main.go");

  // Something else rewrites the file: the clean tab shows the new version.
  await aosd(page, (m) => m.write("/home/aos/project/main.go", "package main // from the Terminal\n"));
  await expect(win.locator(".cm-content")).toContainText("from the Terminal");

  // Now with unsaved edits: the tab asks instead of overwriting either side.
  await typeAtEnd(page, win, "// mine");
  await aosd(page, (m) => m.write("/home/aos/project/main.go", "package main // theirs\n"));
  const ask = win.getByRole("alertdialog", { name: "The file changed on disk" });
  await expect(ask).toBeVisible();
  await expect(win.locator(".cm-content")).toContainText("// mine");
  await ask.getByRole("button", { name: "Take Theirs" }).click();
  await expect(win.locator(".cm-content")).toContainText("// theirs");
  await expect(win.locator(".cm-content")).not.toContainText("// mine");

  // A save over a version it never saw asks too, and Overwrite with Mine wins.
  await typeAtEnd(page, win, "// mine again");
  await aosd(page, (m) => m.write("/home/aos/project/main.go", "package main // theirs again\n"));
  await expect(ask).toBeVisible();
  await ask.getByRole("button", { name: "Overwrite with Mine" }).click();
  await expect.poll(() => disk(page, "/home/aos/project/main.go")).toBe("package main // theirs\n// mine again");

  // A file deleted on disk keeps its tab, crossed out; saving creates it again.
  await aosd(page, (m) => m.remove("/home/aos/project/main.go"));
  await expect(win.getByText("main.go was deleted on disk")).toBeVisible();
  await win.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+s");
  await expect.poll(() => disk(page, "/home/aos/project/main.go")).toBe("package main // theirs\n// mine again");
});

test("closing a window with unsaved files asks Save All, Don't Save or Cancel", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await autoSaveOff(win);
  await openFile(win, "main.go");
  await typeAtEnd(page, win, "// one");
  await openFile(win, "README.md");
  await typeAtEnd(page, win, "two");

  await win.locator(".traffic__close").click();
  const ask = page.getByRole("alertdialog", { name: "Save the changes to 2 files?" });
  await expect(ask).toContainText("main.go");
  await ask.getByRole("button", { name: "Cancel" }).click();
  await expect(win).toBeVisible();

  await win.locator(".traffic__close").click();
  await ask.getByRole("button", { name: "Save All" }).click();
  await expect(win).toHaveCount(0);
  expect(await disk(page, "/home/aos/project/main.go")).toBe("package main\n\nfunc main() {}\n// one");
  expect(await disk(page, "/home/aos/project/README.md")).toBe("# Project\ntwo");
  // Closing the window closed its Workspace on aosd.
  await expect.poll(() => aosd(page, (m) => Object.values(m.workspaces).some((w) => w.open))).toBe(false);

  // Closing one tab asks about that file alone; Don't Save drops the edit.
  const again = await openProject(page);
  await autoSaveOff(again);
  await openFile(again, "main.go");
  await typeAtEnd(page, again, "// dropped");
  await again.getByRole("button", { name: "main.go has unsaved changes; close" }).click();
  await page.getByRole("alertdialog", { name: "Save the changes to main.go?" }).getByRole("button", { name: "Don’t Save" }).click();
  await expect(again.getByRole("tab")).toHaveCount(0);
  expect(await disk(page, "/home/aos/project/main.go")).not.toContain("dropped");
});

test("the Explorer makes, renames and trashes files, and follows changes on disk", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);

  await win.getByRole("button", { name: "New File" }).click();
  await win.getByLabel("New file name").fill("app.py");
  await win.getByLabel("New file name").press("Enter");
  await expect(win.getByRole("tab", { name: /app\.py/, selected: true })).toBeVisible();
  expect(await disk(page, "/home/aos/project/app.py")).toBe("");

  await win.getByRole("button", { name: "New Folder" }).click();
  await win.getByLabel("New folder name").fill("docs");
  await win.getByLabel("New folder name").press("Enter");
  await expect(win.getByRole("treeitem", { name: /docs/ })).toBeVisible();

  // Rename carries the open tab along.
  await win.getByRole("treeitem", { name: /app\.py/ }).click({ button: "right" });
  await page.getByRole("button", { name: "Rename…" }).click();
  await win.getByLabel("New name").fill("server.py");
  await win.getByLabel("New name").press("Enter");
  await expect(win.getByRole("tab", { name: /server\.py/ })).toBeVisible();
  expect(await disk(page, "/home/aos/project/server.py")).toBe("");

  await win.getByRole("treeitem", { name: /docs/ }).click({ button: "right" });
  await page.getByRole("button", { name: "Move to Trash" }).click();
  await expect(win.getByRole("treeitem", { name: /docs/ })).toHaveCount(0);

  // Something else adds a file: the tree shows it.
  await aosd(page, (m) => m.write("/home/aos/project/made-by-agent.txt", "hi\n"));
  await expect(win.getByRole("treeitem", { name: /made-by-agent\.txt/ })).toBeVisible();
});

test("a Workspace is opened again when the tab's event stream reconnects", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await openFile(win, "main.go");
  const first = await aosd(page, (m) => m.subscriptions.at(-1));

  await aosd(page, (m) => m.dropStream());
  await expect.poll(() => aosd(page, (m) => m.subscriptions.length), { timeout: 10_000 }).toBe(2);
  await expect
    .poll(() => aosd(page, (m, [was]) => Object.values(m.workspaces).some((w) => w.open && w.sub !== was), first!), { timeout: 10_000 })
    .toBe(true);

  // Changes still arrive, on the new subscription.
  await aosd(page, (m) => m.write("/home/aos/project/main.go", "package main // after the reconnect\n"));
  await expect(win.locator(".cm-content")).toContainText("after the reconnect");
});

test("Finder's Open in Code opens a folder, or a file with its folder", async ({ page }) => {
  await boot(page);
  await page.locator('.dock__tile[title="Finder"]').click();
  const finder = page.locator('.window[aria-label="Finder"]');
  await finder.locator(".finder__sidebar").getByText("Home").click();
  await finder.getByText("project", { exact: true }).click({ button: "right" });
  await page.getByRole("button", { name: "Open in Code" }).click();
  const win = page.locator('.window[aria-label="project"]');
  await expect(win.getByRole("treeitem", { name: /main\.go/ })).toBeVisible();
  await win.locator(".traffic__close").click(); // it sits over the Finder

  await finder.getByText("notes.md", { exact: true }).click({ button: "right" });
  await page.getByRole("button", { name: "Open in Code" }).click();
  const home = page.locator('.window[aria-label="aos"]');
  await expect(home.getByRole("tab", { name: /notes\.md/, selected: true })).toBeVisible();
  await expect(home.locator(".cm-content")).toContainText("line one");
});

test("Spotlight's Open Folder in Code asks for a folder", async ({ page }) => {
  await boot(page);
  await page.locator(".menubar__app").click();
  await page.keyboard.press("Alt+Space");
  const input = page.getByLabel("Spotlight search");
  if (!(await input.isVisible())) await page.keyboard.press("Control+Space");
  await input.fill("open folder");
  await page.locator(".spot__row").filter({ hasText: "Open Folder in Code…" }).click();
  await expect(page.locator('.window[aria-label="Code"]').getByLabel("Folder to open")).toBeVisible();
});

test("in Root Mode Auto Save starts off, and files open read-only until M8.5", async ({ page }) => {
  await boot(page, { rootMode: true });
  const win = await openProject(page);
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Root Mode · root");
  await expect(win.getByRole("button", { name: "Auto Save: Off" })).toBeVisible();
  await openFile(win, "main.go");
  await expect(win.getByText(/Read-only in Root Mode/)).toBeVisible();
});

test("TextEdit still edits and saves a file on the shared editor", async ({ page }) => {
  await boot(page);
  await page.locator('.dock__tile[title="Finder"]').click();
  const finder = page.locator('.window[aria-label="Finder"]');
  await finder.locator(".finder__sidebar").getByText("Home").click();
  await finder.getByText("notes.md", { exact: true }).dblclick();
  const te = page.locator('.window[aria-label="notes.md"]');
  await expect(te.locator(".cm-content")).toContainText("line one");
  await te.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("line two");
  await page.keyboard.press("ControlOrMeta+s");
  await expect.poll(() => disk(page, "/home/aos/notes.md")).toBe("line one\nline two");
});
