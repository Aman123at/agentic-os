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
  await expect(win.getByRole("tablist", { name: "Open files" }).getByRole("tab")).toHaveCount(2);
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
  await expect(again.getByRole("tablist", { name: "Open files" }).getByRole("tab")).toHaveCount(0);
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

test("in Root Mode Code edits as root: Auto Save starts off, and AOS's own state is read-only", async ({ page }) => {
  await boot(page, { rootMode: true, files: { ...PROJECT, "/var/lib/aos/aos.db": "sqlite" } });
  const win = await openProject(page);
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Root Mode · root");
  await expect(win.getByRole("button", { name: "Auto Save: Off" })).toBeVisible();
  await openFile(win, "main.go");
  await typeAtEnd(page, win, "// as root");
  await page.keyboard.press("ControlOrMeta+s");
  await expect.poll(() => disk(page, "/home/aos/project/main.go")).toContain("// as root");
  await win.locator(".traffic__close").click();

  await page.locator('.dock__tile[title="Code"]').click();
  await page.getByLabel("Folder to open").fill("/var/lib/aos");
  await page.getByRole("button", { name: "Open" }).click();
  const state = page.locator('.window[aria-label="aos"]');
  await openFile(state, "aos.db");
  await expect(state.getByText(/this is AOS’s own state/)).toBeVisible();
});

const SUDOERS = { ...PROJECT, "/etc/sudoers": "root ALL=(ALL) ALL\n", "/etc/sudoers.d/": "", "/etc/motd": "hello\n" };

async function openEtc(page: Page): Promise<Locator> {
  await page.locator('.dock__tile[title="Code"]').click();
  await page.getByLabel("Folder to open").fill("/etc");
  await page.getByRole("button", { name: "Open" }).click();
  const win = page.locator('.window[aria-label="etc"]');
  await expect(win.getByRole("treeitem", { name: /sudoers$/ })).toBeVisible();
  return win;
}

test("a Sensitive System File warns on open, asks with its check on save, and never auto-saves", async ({ page }) => {
  await boot(page, { rootMode: true, files: SUDOERS });
  const win = await openEtc(page);
  await win.getByRole("button", { name: "Auto Save: Off" }).click(); // on, to show it leaves sudoers alone
  await openFile(win, "sudoers");
  const banner = win.getByRole("note", { name: "Sensitive System File" });
  await expect(banner).toContainText("lock everyone out of sudo");

  // Auto Save leaves it alone.
  await typeAtEnd(page, win, "aos ALL=(ALL) NOPASSWD: ALL\n");
  await page.waitForTimeout(1600);
  expect(await disk(page, "/etc/sudoers")).toBe("root ALL=(ALL) ALL\n");

  // ⌘S asks, naming the file and the risk, after visudo's check.
  await page.keyboard.press("ControlOrMeta+s");
  const ask = page.getByRole("alertdialog", { name: "Save /etc/sudoers?" });
  await expect(ask).toContainText("lock everyone out of sudo");
  await expect(ask).toContainText("AOS can’t undo this");
  await expect(ask).toContainText("✓ visudo -c found no problems.");
  await ask.getByRole("button", { name: "Cancel" }).click();
  expect(await disk(page, "/etc/sudoers")).toBe("root ALL=(ALL) ALL\n");

  await page.keyboard.press("ControlOrMeta+s");
  await ask.getByRole("button", { name: "Save anyway" }).click();
  await expect.poll(() => disk(page, "/etc/sudoers")).toBe("root ALL=(ALL) ALL\naos ALL=(ALL) NOPASSWD: ALL\n");
  expect(await aosd(page, (m) => m.sensitiveWrites)).toEqual(["/etc/sudoers"]);

  // The version it replaced can be put back.
  await banner.getByRole("button", { name: "Restore Previous Version" }).click();
  await page.getByRole("alertdialog", { name: /Restore the previous version of \/etc\/sudoers/ }).getByRole("button", { name: "Restore" }).click();
  await expect.poll(() => disk(page, "/etc/sudoers")).toBe("root ALL=(ALL) ALL\n");
  await expect(win.locator(".cm-content")).not.toContainText("NOPASSWD");

  // An ordinary file in /etc saves without asking.
  await openFile(win, "motd");
  await expect(win.getByRole("note", { name: "Sensitive System File" })).toHaveCount(0);
});

test("a failed check turns the button into Save anyway (check failed)", async ({ page }) => {
  await boot(page, { rootMode: true, files: SUDOERS });
  const win = await openEtc(page);
  await openFile(win, "sudoers");
  await win.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("oops");
  await page.keyboard.press("ControlOrMeta+s");
  const ask = page.getByRole("alertdialog", { name: "Save /etc/sudoers?" });
  await expect(ask).toContainText("✗ visudo -c found problems:");
  await expect(ask).toContainText("syntax error");
  await ask.getByRole("button", { name: "Save anyway (check failed)" }).click();
  await expect.poll(() => disk(page, "/etc/sudoers")).toBe("oops");
});

test("a new file in sudoers.d asks before aosd creates it", async ({ page }) => {
  await boot(page, { rootMode: true, files: SUDOERS });
  const win = await openEtc(page);
  await win.getByRole("treeitem", { name: /sudoers\.d/ }).click({ button: "right" });
  await page.getByRole("button", { name: "New File…" }).click();
  await win.getByLabel("New file name").fill("90-aos");
  await win.getByLabel("New file name").press("Enter");
  const ask = page.getByRole("alertdialog", { name: "Create /etc/sudoers.d/90-aos?" });
  await expect(ask).toContainText("lock everyone out of sudo");
  await ask.getByRole("button", { name: "Create anyway" }).click();
  await expect(win.getByRole("tab", { name: /90-aos/, selected: true })).toBeVisible();
  expect(await disk(page, "/etc/sudoers.d/90-aos")).toBe("");
});

test("in Standard Mode a file aos can't write opens read-only, with a lock and why", async ({ page }) => {
  await boot(page, { files: { ...PROJECT, "/etc/hosts": "127.0.0.1 localhost\n", "/home/aos/project/locked.txt": "keep\n" }, readOnly: ["/home/aos/project/locked.txt"] });
  await page.locator('.dock__tile[title="Code"]').click();
  await page.getByLabel("Folder to open").fill("/etc");
  await page.getByRole("button", { name: "Open" }).click();
  const win = page.locator('.window[aria-label="etc"]');
  await openFile(win, "hosts");
  await expect(win.getByText("Read-only in Standard Mode — aos can’t write this file.")).toBeVisible();
  await expect(win.getByRole("tab", { name: /hosts/ })).toContainText("🔒");
  await win.locator(".cm-content").click();
  await page.keyboard.type("evil");
  await page.keyboard.press("ControlOrMeta+s");
  await expect(win.locator(".cm-content")).not.toContainText("evil");
  expect(await disk(page, "/etc/hosts")).toBe("127.0.0.1 localhost\n");

  // Inside the home folder too, what aos can't write stays read-only.
  await win.locator(".traffic__close").click();
  const project = await openProject(page);
  await openFile(project, "locked.txt");
  await expect(project.getByText("Read-only in Standard Mode")).toBeVisible();
  await openFile(project, "main.go");
  await expect(project.getByText("Read-only in Standard Mode")).toHaveCount(0);
});

test("in Root Mode TextEdit and Finder ask before changing a Sensitive System File", async ({ page }) => {
  await boot(page, { rootMode: true, files: SUDOERS });
  await page.locator('.dock__tile[title="Finder"]').click();
  const finder = page.locator('.window[aria-label="Finder"]');
  await finder.locator(".finder__sidebar").getByText("Filesystem").click();
  await finder.getByText("etc", { exact: true }).dblclick();

  // Finder: Move to Trash asks, and Cancel leaves it be.
  await finder.getByText("sudoers", { exact: true }).click({ button: "right" });
  await page.getByRole("button", { name: "Move to Trash" }).click();
  const trashAsk = page.getByRole("alertdialog", { name: "Move to Trash /etc/sudoers?" });
  await expect(trashAsk).toContainText("lock everyone out of sudo");
  await trashAsk.getByRole("button", { name: "Cancel" }).click();
  expect(await disk(page, "/etc/sudoers")).toBe("root ALL=(ALL) ALL\n");

  // TextEdit: saving asks, with the check, and saves once confirmed.
  await finder.getByText("sudoers", { exact: true }).dblclick();
  const te = page.locator('.window[aria-label="sudoers"]');
  await expect(te.locator(".cm-content")).toContainText("root ALL");
  await te.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("aos ALL=(ALL) ALL");
  await page.keyboard.press("ControlOrMeta+s");
  const ask = te.getByRole("alertdialog", { name: "Save /etc/sudoers?" });
  await expect(ask).toContainText("✓ visudo -c found no problems.");
  await ask.getByRole("button", { name: "Save anyway" }).click();
  await expect.poll(() => disk(page, "/etc/sudoers")).toBe("root ALL=(ALL) ALL\naos ALL=(ALL) ALL");
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

// termText is what the newest xterm shows (term.ts hands it over under test).
const termText = (page: Page) =>
  page.evaluate(() => {
    const t = (window as unknown as { __aosTerm?: { buffer: { active: { length: number; getLine(i: number): { translateToString(trim: boolean): string } | undefined } } } }).__aosTerm;
    if (!t) return "";
    const b = t.buffer.active;
    return Array.from({ length: b.length }, (_, i) => b.getLine(i)?.translateToString(true) ?? "").join("\n");
  });

test("⌃` opens a Terminal panel in the Workspace; it survives hiding and ends with the window", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  const panel = win.getByRole("region", { name: "Terminal panel" });
  await expect(panel).toHaveCount(0); // xterm loads only when asked for

  await win.getByText("Open a file from the Explorer.").click();
  await page.keyboard.press("Control+Backquote");
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("tab", { name: /Terminal 1/ })).toBeVisible();
  await expect.poll(() => aosd(page, (m) => Object.values(m.sessions).map((s) => s.cwd))).toEqual(["/home/aos/project"]);
  await expect(panel).not.toContainText("root");

  await expect.poll(() => termText(page)).toContain("aos$");
  await panel.locator(".xterm").click();
  await page.keyboard.type("pwd\r");
  await expect.poll(() => termText(page)).toContain("/home/aos/project");

  // Hiding keeps the shell; showing it again opens no new one.
  await page.keyboard.press("Control+Backquote");
  await expect(panel).toBeHidden();
  await win.getByRole("button", { name: /Terminal/ }).click();
  await expect(panel).toBeVisible();
  expect(await aosd(page, (m) => Object.keys(m.sessions).length)).toBe(1);

  // A second Terminal, then closing the window ends both.
  await panel.getByRole("button", { name: "New Terminal" }).click();
  await expect(panel.getByRole("tab")).toHaveCount(2);
  await win.locator(".traffic__close").click();
  await expect(win).toHaveCount(0);
  await expect.poll(() => aosd(page, (m) => Object.values(m.sessions).map((s) => s.open))).toEqual([false, false]);
});

test("a reload takes the panel's shell back rather than starting another", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await win.getByRole("button", { name: /Terminal/ }).click();
  const panel = win.getByRole("region", { name: "Terminal panel" });
  await expect(panel.getByRole("tab", { name: /Terminal 1/ })).toBeVisible();
  const first = await aosd(page, (m) => Object.keys(m.sessions));
  expect(first).toHaveLength(1);

  // The fake Machine is reborn with the page, so carry the Session over.
  await page.evaluate(() => sessionStorage.setItem("fake.sessions", JSON.stringify((window as unknown as { __aosd: FakeMachine }).__aosd.sessions)));
  await page.reload();
  const again = page.locator('.window[aria-label="project"]').getByRole("region", { name: "Terminal panel" });
  await expect(again.getByRole("tab", { name: /Terminal 1/ })).toBeVisible();
  expect(await aosd(page, (m) => Object.keys(m.sessions))).toEqual(first);
});

test("in Root Mode the panel's shell is root's, and says so", async ({ page }) => {
  await boot(page, { rootMode: true });
  const win = await openProject(page);
  await win.getByRole("button", { name: /Terminal/ }).click();
  const panel = win.getByRole("region", { name: "Terminal panel" });
  await expect(panel.getByText("this Terminal runs as root")).toBeVisible();
  await expect.poll(() => termText(page)).toContain("root@machine:/home/aos/project#");
});

test("⌘P finds a file by a few letters and opens it, at a line if asked", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await win.getByText("Open a file from the Explorer.").click();

  await page.keyboard.press("ControlOrMeta+p");
  const palette = win.getByRole("dialog", { name: "Quick Open" });
  await expect(palette).toBeVisible();
  await page.keyboard.type("utl");
  const first = palette.getByRole("option").first();
  await expect(first).toContainText("util.ts");
  await expect(first).toHaveAttribute("aria-selected", "true");
  await expect(first.locator("b").first()).toHaveText("ut"); // the letters that matched
  await page.keyboard.press("Enter");
  await expect(palette).toHaveCount(0);
  await expect(win.getByRole("tab", { name: /util\.ts/, selected: true })).toBeVisible();

  // name:line goes to the line.
  await expect(win.locator(".cm-content")).toBeFocused();
  await page.keyboard.press("ControlOrMeta+p");
  await expect(palette).toBeVisible();
  await page.keyboard.type("main:3");
  await expect(palette.getByRole("option").first()).toContainText("main.go");
  await page.keyboard.press("Enter");
  await expect(win.getByRole("tab", { name: /main\.go/, selected: true })).toBeVisible();
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Ln 3, Col 1");

  // Escape closes it without opening anything.
  await page.keyboard.press("ControlOrMeta+p");
  await page.keyboard.press("Escape");
  await expect(palette).toHaveCount(0);
  await expect(win.getByRole("tablist", { name: "Open files" }).getByRole("tab")).toHaveCount(2);
});

test("⌘⇧F searches the Workspace and a result opens the file at the match", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await win.getByText("Open a file from the Explorer.").click();

  await page.keyboard.press("ControlOrMeta+Shift+f");
  const search = win.getByLabel("Search for");
  await expect(search).toBeFocused();
  await page.keyboard.type("main");
  const summary = win.getByRole("status", { name: "Search results summary" });
  await expect(summary).toHaveText("2 results in 1 file");
  const hit = win.getByRole("treeitem", { name: "main.go line 3" });
  await expect(hit.locator("mark")).toHaveText("main");
  await hit.click();
  await expect(win.getByRole("tab", { name: /main\.go/, selected: true })).toBeVisible();
  // The match is selected: the cursor sits at its end.
  await expect(win.getByRole("status", { name: "Status bar" })).toContainText("Ln 3, Col 10");

  // Options: case, and a regular expression that does not compile says why.
  await win.getByRole("button", { name: "Aa" }).click();
  await search.fill("MAIN");
  await expect(summary).toHaveText("No results.");
  await win.getByRole("button", { name: ".*" }).click();
  await search.fill("(");
  await expect(summary).toContainText("not valid");

  // The Explorer is a click away, as it was.
  await win.getByRole("tab", { name: "Explorer" }).click();
  await expect(win.getByRole("treeitem", { name: /README\.md/ })).toBeVisible();
});

test("without ripgrep, Find in Files says it searched on its own", async ({ page }) => {
  await boot(page, { engine: "built-in" });
  const win = await openProject(page);
  await win.getByRole("tab", { name: "Search" }).click();
  await win.getByLabel("Search for").fill("Project");
  await expect(win.getByRole("status", { name: "Search results summary" })).toContainText("ripgrep isn’t installed");
});

const NAV = {
  "/home/aos/nav/main.go": "package main\n\nfunc main() {\n\tRun()\n}\n",
  "/home/aos/nav/run.go": "package main\n\n// Run runs.\nfunc Run() {\n\thelper()\n}\n\nfunc helper() {}\n",
  "/home/aos/nav/web/app.ts": "export function Run() {}\nRun()\n",
  "/home/aos/nav/web/other.ts": "export function Run() {}\n",
};

async function openNav(page: Page): Promise<Locator> {
  await page.locator('.dock__tile[title="Code"]').click();
  const asking = page.locator('.window[aria-label="Code"]');
  await asking.getByLabel("Folder to open").fill("~/nav");
  await asking.getByRole("button", { name: "Open" }).click();
  const win = page.locator('.window[aria-label="nav"]');
  await expect(win.getByRole("treeitem", { name: /main\.go/ })).toBeVisible();
  return win;
}

// goTo puts the cursor on a line with ⌃G, then at column col.
async function goTo(page: Page, line: number, col: number) {
  await page.keyboard.press("Control+g");
  await page.keyboard.type(`:${line}`.slice(1));
  await page.keyboard.press("Enter");
  // The editor takes focus back once the palette has closed.
  await page.waitForFunction(() => document.activeElement?.classList.contains("cm-content"));
  for (let i = 1; i < col; i++) await page.keyboard.press("ArrowRight");
}

test("F12 goes to a definition in another file, and ⌃- comes back", async ({ page }) => {
  await boot(page, { files: NAV });
  const win = await openNav(page);
  const status = win.getByRole("status", { name: "Status bar" });
  await openFile(win, "main.go");
  await goTo(page, 4, 4); // \tRu|n()
  await expect(status).toContainText("Ln 4, Col 4");

  await page.keyboard.press("F12");
  await expect(win.getByRole("tab", { name: /run\.go/, selected: true })).toBeVisible();
  await expect(status).toContainText("Ln 4, Col 9"); // Run selected in func Run() {

  await page.keyboard.press("Control+Minus");
  await expect(win.getByRole("tab", { name: /main\.go/, selected: true })).toBeVisible();
  await expect(status).toContainText("Ln 4, Col 4");
  await page.keyboard.press("Control+Shift+Minus");
  await expect(win.getByRole("tab", { name: /run\.go/, selected: true })).toBeVisible();
  await expect(status).toContainText("Ln 4");

  // ⌘-click does the same.
  await win.locator(".cm-line", { hasText: "helper()" }).first().getByText("helper").click({ modifiers: ["ControlOrMeta"] });
  await expect(status).toContainText("Ln 8, Col 12");

  // A name defined several times offers each.
  await win.getByRole("treeitem", { name: /📁 web/ }).click();
  await openFile(win, "app.ts");
  await goTo(page, 2, 2);
  await page.keyboard.press("F12");
  const pick = win.getByRole("dialog", { name: "2 definitions of Run" }); // the TypeScript ones
  await expect(pick.getByRole("option")).toHaveCount(2);
  await pick.getByRole("option", { name: /other\.ts/ }).click();
  await expect(win.getByRole("tab", { name: /other\.ts/, selected: true })).toBeVisible();
});

test("⇧F12, ⌘⇧O, ⌘T and the Outline find symbols and their uses", async ({ page }) => {
  await boot(page, { files: NAV });
  const win = await openNav(page);
  const status = win.getByRole("status", { name: "Status bar" });
  await openFile(win, "run.go");

  // References: whole words, in Go files.
  await goTo(page, 4, 7);
  await page.keyboard.press("Shift+F12");
  const refs = win.getByRole("dialog", { name: "3 references to Run" });
  await expect(refs.getByRole("option")).toHaveCount(3);
  await refs.getByRole("option", { name: /main\.go:4/ }).click();
  await expect(win.getByRole("tab", { name: /main\.go/, selected: true })).toBeVisible();

  // The file's symbols (⌘⇧O): the palette in @ mode.
  await openFile(win, "run.go");
  await page.keyboard.press("ControlOrMeta+Shift+o");
  const palette = win.getByRole("dialog", { name: "Quick Open" });
  await expect(palette.getByRole("option")).toHaveCount(2);
  await page.keyboard.type("hel");
  await page.keyboard.press("Enter");
  await expect(status).toContainText("Ln 8");

  // Symbols anywhere: ⌘P then #.
  await openFile(win, "main.go");
  await page.keyboard.press("ControlOrMeta+p");
  await page.keyboard.type("#helper");
  await expect(palette.getByRole("option").first()).toContainText("run.go:8");
  await page.keyboard.press("Enter");
  await expect(win.getByRole("tab", { name: /run\.go/, selected: true })).toBeVisible();

  // The Outline lists the file in front's symbols.
  await win.getByRole("tab", { name: "Outline" }).click();
  const outline = win.getByRole("tree", { name: "Symbols" });
  await expect(outline.getByRole("treeitem")).toHaveCount(2);
  await outline.getByRole("treeitem", { name: "func Run" }).click();
  await expect(status).toContainText("Ln 4");
});

test("completion offers the Workspace's symbols and the language's keywords", async ({ page }) => {
  await boot(page, { files: NAV });
  const win = await openNav(page);
  await openFile(win, "main.go");
  await autoSaveOff(win);
  await goTo(page, 4, 7);
  await page.keyboard.press("Enter");
  await page.keyboard.type("hel");
  const menu = page.locator(".cm-tooltip-autocomplete");
  await expect(menu).toContainText("helper");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Enter");
  await page.keyboard.type("defe");
  await expect(menu).toContainText("defer");
});

test("without universal-ctags, F12 says to install it", async ({ page }) => {
  await boot(page, { files: NAV, noCtags: true });
  const win = await openNav(page);
  await openFile(win, "main.go");
  await goTo(page, 4, 3);
  await page.keyboard.press("F12");
  await expect(win.getByRole("status", { name: "Notice" })).toContainText("sudo apt install universal-ctags");
});
