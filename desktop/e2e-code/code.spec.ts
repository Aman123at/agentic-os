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

test("a script is highlighted by its #! line, in Code and TextEdit, and the status bar names it", async ({ page }) => {
  await boot(page, {
    files: {
      ...PROJECT,
      "/home/aos/project/deploy": "#!/usr/bin/env python3\ndef main():\n    pass\n",
      "/home/aos/project/Makefile": "all:\n\tgo build ./...\n",
    },
  });
  const win = await openProject(page);
  const status = win.getByRole("status", { name: "Status bar" });
  await openFile(win, "deploy");
  await expect(status).toContainText("Python");
  // Highlighted runs are spans; plain text is not.
  await expect(win.locator(".cm-line span", { hasText: /^def$/ })).toBeVisible();
  await openFile(win, "Makefile");
  await expect(status).toContainText("Makefile");
  await expect(win.locator(".cm-line span", { hasText: /^all$/ })).toBeVisible();

  await page.locator('.dock__tile[title="Finder"]').click();
  const finder = page.locator('.window[aria-label="Finder"]');
  await finder.locator(".finder__sidebar").getByText("Home").click();
  await finder.getByText("project", { exact: true }).dblclick();
  await finder.getByText("deploy", { exact: true }).dblclick();
  const te = page.locator('.window[aria-label="deploy"]');
  await expect(te.locator(".cm-line span", { hasText: /^def$/ })).toBeVisible();
});

// Seeing your own changes (M8.10).
const LIST = "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n";

// toLine puts the cursor at the end of a line of the editor in front.
async function toLine(page: Page, win: Locator, n: number) {
  await win.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+Home");
  for (let i = 1; i < n; i++) await page.keyboard.press("ArrowDown");
  await page.keyboard.press("End");
}

const marks = (win: Locator, kind: string) => win.locator(`.cm-savedGutter .cm-change--${kind}`);

test("the gutter marks lines changed since saved, and Compare with Saved reverts them", async ({ page }) => {
  await boot(page, { files: { ...PROJECT, "/home/aos/project/list.txt": LIST } });
  const win = await openProject(page);
  await autoSaveOff(win);
  await openFile(win, "list.txt");

  // c changed, a line added after e, i deleted.
  await toLine(page, win, 3);
  await page.keyboard.press("Backspace");
  await page.keyboard.type("C");
  await toLine(page, win, 5);
  await page.keyboard.press("Enter");
  await page.keyboard.type("new");
  await toLine(page, win, 10);
  await page.keyboard.press("Home");
  await page.keyboard.press("Shift+ArrowDown");
  await page.keyboard.press("Backspace");
  await page.keyboard.press("Escape");
  await expect(marks(win, "changed")).toHaveCount(1);
  await expect(marks(win, "added")).toHaveCount(1);
  await expect(marks(win, "deleted")).toHaveCount(1);

  // Compare with Saved shows the three; one taken back goes back in the tab.
  // (The Dock covers the middle of the status bar in the test's window.)
  await win.getByRole("status", { name: "Status bar" }).getByRole("button", { name: "Compare with Saved" }).dispatchEvent("click");
  const cmp = win.getByRole("region", { name: "list.txt compared with saved" });
  await expect(cmp).toContainText("3 differences");
  await cmp.getByRole("button", { name: "Revert to saved" }).first().click();
  await expect(cmp).toContainText("2 differences");
  await cmp.getByRole("button", { name: "Close" }).click();
  await expect(cmp).toBeHidden();
  await expect(win.locator(".cm-content")).toContainText("c");
  await expect(marks(win, "changed")).toHaveCount(0);
  await expect(marks(win, "added")).toHaveCount(1);

  // A mark in the gutter opens the compare too; Revert All leaves the tab
  // as saved.
  await marks(win, "added").click();
  await expect(cmp).toContainText("2 differences");
  await cmp.getByRole("button", { name: "Revert All" }).click();
  await expect(cmp).toContainText("No changes since saved.");
  await cmp.getByRole("button", { name: "Close" }).click();
  await expect(win.getByRole("button", { name: "Close list.txt" })).toBeVisible();
  await expect(win.locator(".cm-savedGutter .cm-change")).toHaveCount(0);

  // Saving makes what was saved the new mark of nothing changed.
  await typeAtEnd(page, win, "k"); // on the empty last line: changed
  await page.keyboard.press("Escape");
  await expect(marks(win, "changed")).toHaveCount(1);
  await page.keyboard.press("ControlOrMeta+s");
  await expect.poll(() => disk(page, "/home/aos/project/list.txt")).toBe(LIST + "k");
  await expect(win.locator(".cm-savedGutter .cm-change")).toHaveCount(0);
});

test("a change on disk under unsaved edits is settled hunk by hunk: Keep mine, Take theirs", async ({ page }) => {
  await boot(page, { files: { ...PROJECT, "/home/aos/project/list.txt": LIST } });
  const win = await openProject(page);
  await autoSaveOff(win);
  await openFile(win, "list.txt");
  await toLine(page, win, 2);
  await page.keyboard.type(" mine");
  await toLine(page, win, 8);
  await page.keyboard.type(" mine");
  await page.keyboard.press("Escape");

  await aosd(page, (m) => m.write("/home/aos/project/list.txt", "a\nb theirs\nc\nd\ne\nf\ng\nh theirs\ni\nj\nk\n"));
  const ask = win.getByRole("alertdialog", { name: "The file changed on disk" });
  await ask.getByRole("button", { name: "Compare…" }).click();
  const cmp = win.getByRole("region", { name: "list.txt changed on disk" });
  await expect(cmp).toContainText("3 differences");

  // Keep mine for b, take theirs for h, and the rest (their k) stays mine.
  await cmp.getByRole("button", { name: "Keep mine" }).first().click();
  await expect(cmp).toContainText("2 differences");
  await cmp.getByRole("button", { name: "Take theirs" }).first().click();
  await expect(cmp).toContainText("1 difference");
  await cmp.getByRole("button", { name: "Keep the Rest Mine" }).click();
  await expect(cmp).toBeHidden();
  await expect(ask).toBeHidden();
  await expect(win.locator(".cm-content")).toContainText("h theirs");
  await expect(win.locator(".cm-content")).toContainText("b mine");

  // Settled: the save goes through without asking again.
  await page.keyboard.press("ControlOrMeta+s");
  await expect.poll(() => disk(page, "/home/aos/project/list.txt")).toBe("a\nb mine\nc\nd\ne\nf\ng\nh theirs\ni\nj\n");
  await expect(ask).toBeHidden();

  // Take All Theirs takes every difference left.
  await typeAtEnd(page, win, "mine");
  await page.keyboard.press("Escape");
  await aosd(page, (m) => m.write("/home/aos/project/list.txt", "all theirs\n"));
  await ask.getByRole("button", { name: "Compare…" }).click();
  await cmp.getByRole("button", { name: "Take All Theirs" }).click();
  await expect(cmp).toContainText("All differences decided.");
  await cmp.getByRole("button", { name: "Done" }).click();
  await expect(win.locator(".cm-content")).toHaveText("all theirs");
  await expect(win.getByRole("button", { name: "Close list.txt" })).toBeVisible();
});

// The Agent in Code (M8.11).
test("the Agent sidebar starts a Task with where you are working, and ⌘L adds the selection", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await openFile(win, "README.md");
  await openFile(win, "main.go");

  // ⌘L: the line goes to the chat, and the sidebar opens with the composer ready.
  await toLine(page, win, 3);
  await page.keyboard.press("Shift+Home");
  await page.keyboard.press("ControlOrMeta+l");
  const agent = win.getByRole("complementary", { name: "Agent" });
  await expect(agent).toBeVisible();
  await expect(agent.getByText("main.go:3")).toBeVisible();
  await expect(agent.getByRole("textbox", { name: "Message the Agent" })).toBeFocused();

  await aosd(page, (m) => void m.agent.push({ say: "`main` does nothing yet." }));
  await page.keyboard.type("What does main do?");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(agent.getByText("does nothing yet.")).toBeVisible();
  await expect(agent.getByRole("combobox", { name: "Chat" })).toHaveValue(/^t_/);
  await expect(agent.getByRole("combobox", { name: "Chat" }).locator("option:checked")).toHaveText("What does main do? — Done");
  await expect(agent.getByText("main.go:3")).toBeHidden();

  const sent = await aosd(page, (m) => JSON.stringify(m.taskRequests));
  const [create] = JSON.parse(sent) as { method: string; body: { prompt: string; editor: Record<string, unknown> } }[];
  expect(create.method).toBe("CreateTask");
  expect(create.body.editor).toEqual({
    workspace: "/home/aos/project",
    activeFile: "/home/aos/project/main.go",
    openFiles: ["/home/aos/project/README.md", "/home/aos/project/main.go"],
    selection: "func main() {}",
    selectionStartLine: 3,
    selectionEndLine: 3,
  });

  // A Follow-up carries where the user is now.
  await aosd(page, (m) => void m.agent.push({ say: "Glad to help." }));
  await openFile(win, "README.md");
  const reply = agent.getByRole("textbox", { name: "Message the Agent" });
  await reply.fill("Thanks");
  await reply.press("ControlOrMeta+Enter");
  await expect(agent.getByText("Glad to help.")).toBeVisible();
  const follow = JSON.parse(await aosd(page, (m) => JSON.stringify(m.taskRequests[1]))) as { method: string; body: { editor: Record<string, unknown> } };
  expect(follow.method).toBe("SendFollowUp");
  expect(follow.body.editor).toMatchObject({ workspace: "/home/aos/project", activeFile: "/home/aos/project/README.md" });

  // New chat, and the Workspace's chats listed; the sidebar hides and comes back.
  await agent.getByRole("button", { name: "New chat" }).click();
  await expect(agent.getByRole("combobox", { name: "Chat" })).toHaveValue("");
  await expect(agent.getByRole("combobox", { name: "Chat" }).locator("option")).toHaveCount(2);
  await agent.getByRole("button", { name: "Hide the Agent" }).click();
  await expect(agent).toBeHidden();
  await win.getByRole("status", { name: "Status bar" }).getByRole("button", { name: "✦ Agent" }).dispatchEvent("click");
  await expect(agent).toBeVisible();
});

// Change Review (M8.12).
async function askAgent(page: Page, win: Locator, prompt: string) {
  const agent = win.getByRole("complementary", { name: "Agent" });
  if (!(await agent.isVisible())) await win.getByRole("status", { name: "Status bar" }).getByRole("button", { name: "✦ Agent" }).dispatchEvent("click");
  const box = agent.getByRole("textbox", { name: "Message the Agent" });
  await box.fill(prompt);
  await box.press("ControlOrMeta+Enter");
  return agent;
}

test("Change Review keeps and discards the Agent's hunks, and what is left survives closing the window", async ({ page }) => {
  await boot(page, { files: { ...PROJECT, "/home/aos/project/list.txt": LIST } });
  let win = await openProject(page);
  await openFile(win, "list.txt");
  const theirs = LIST.replace("b\n", "B\n").replace("h\n", "H\n");
  await aosd(
    page,
    (m, [list, text]) => void m.agent.push({ write: list, text }, { write: "/home/aos/project/new.txt", text: "fresh\n" }, { say: "Changed the list." }),
    "/home/aos/project/list.txt",
    theirs,
  );
  let agent = await askAgent(page, win, "Capitalise b and h, and add new.txt");
  await expect(agent.getByText("Changed the list.")).toBeVisible();
  // The clean tab took the Agent's version as it wrote it.
  await expect(win.locator(".cm-content")).toContainText("H");

  let changes = agent.getByRole("region", { name: "Changed files" });
  await expect(changes).toContainText("2 files changed — Review");
  await changes.getByRole("button", { name: "list.txt", exact: true }).click();
  const review = win.getByRole("region", { name: "Change Review" });
  await expect(review).toContainText("changes to list.txt — 2 left");
  await win.getByRole("button", { name: "Keep this change" }).first().click();
  await expect(review).toContainText("1 left");
  const kept = JSON.parse(await aosd(page, (m) => JSON.stringify(m.taskRequests.filter((r) => r.method === "ReviewChange")))) as { body: { base: string } }[];
  expect(Buffer.from(kept[0].body.base, "base64").toString()).toBe(LIST.replace("b\n", "B\n"));

  // What is left is aosd's to remember: close the window, open it again.
  await win.locator(".traffic__close").click();
  await expect(win).toBeHidden();
  win = await openProject(page);
  await win.getByRole("status", { name: "Status bar" }).getByRole("button", { name: "✦ Agent" }).dispatchEvent("click");
  agent = win.getByRole("complementary", { name: "Agent" });
  await agent.getByRole("combobox", { name: "Chat" }).selectOption({ label: "Capitalise b and h, and add new.txt — Done" });
  changes = agent.getByRole("region", { name: "Changed files" });
  await expect(changes).toContainText("2 files changed — Review");
  await changes.getByRole("button", { name: "list.txt", exact: true }).click();
  await expect(review).toContainText("changes to list.txt — 1 left");

  // Discard puts h back on disk; with nothing left, the review ends.
  await win.getByRole("button", { name: "Discard this change" }).click();
  await expect.poll(() => disk(page, "/home/aos/project/list.txt")).toBe(LIST.replace("b\n", "B\n"));
  await expect(review).toBeHidden();
  await expect(changes).toContainText("1 file changed — Review");

  // Discarding a file the Agent created moves it to the Trash, and its tab goes.
  await changes.getByRole("button", { name: "new.txt", exact: true }).click();
  await expect(review).toContainText("changes to new.txt — 1 left");
  await win.getByRole("button", { name: "Discard this change" }).click();
  await expect.poll(() => disk(page, "/home/aos/project/new.txt")).toBeNull();
  await expect(win.getByRole("tab", { name: /new\.txt/ })).toBeHidden();
  await expect(changes).toContainText("All 2 changed files are reviewed");
});

test("Keep All and Discard All decide a Task's files whole; a file a command changed is reviewed against what the editor had", async ({ page }) => {
  await boot(page, { files: { ...PROJECT, "/home/aos/project/gen.txt": "old\n" } });
  const win = await openProject(page);
  await openFile(win, "gen.txt");
  await aosd(
    page,
    (m) =>
      void m.agent.push(
        { write: "/home/aos/project/main.go", text: "package main // agent\n" },
        { write: "/home/aos/project/README.md", text: "# Agent\n" },
        { command: "/home/aos/project/gen.txt", text: "generated\n" },
        { say: "Done." },
      ),
  );
  const agent = await askAgent(page, win, "Do three things");
  await expect(agent.getByText("Done.", { exact: true })).toBeVisible();
  const changes = agent.getByRole("region", { name: "Changed files" });
  await expect(changes).toContainText("3 files changed — Review");
  await expect(changes).toContainText("gen.txt by a command");

  // The command's change, against what the tab had: discard it.
  await changes.getByRole("button", { name: /gen\.txt by a command/ }).click();
  const review = win.getByRole("region", { name: "Change Review" });
  await expect(review).toContainText("Reviewing a command’s changes to gen.txt — 1 left");
  await win.getByRole("button", { name: "Discard this change" }).click();
  await expect.poll(() => disk(page, "/home/aos/project/gen.txt")).toBe("old\n");
  await expect(changes).toContainText("2 files changed — Review");

  // Keep one whole, discard the other whole.
  await changes.getByRole("button", { name: "Keep the changes to main.go" }).click();
  await expect(changes).toContainText("1 file changed — Review");
  await changes.getByRole("button", { name: "Discard the changes to README.md" }).click();
  await expect.poll(() => disk(page, "/home/aos/project/README.md")).toBe("# Project\n");
  await expect.poll(() => disk(page, "/home/aos/project/main.go")).toBe("package main // agent\n");
  await expect(changes).toContainText("All 2 changed files are reviewed");
});

// termText is what the newest xterm shows (term.ts hands it over under test).
const termText = (page: Page) =>
  page.evaluate(() => {
    const t = (window as unknown as { __aosTerm?: { buffer: { active: { length: number; getLine(i: number): { translateToString(trim: boolean): string } | undefined } } } }).__aosTerm;
    if (!t) return "";
    const b = t.buffer.active;
    return Array.from({ length: b.length }, (_, i) => b.getLine(i)?.translateToString(true) ?? "").join("\n");
  });

// systemPane opens System Settings from Spotlight, on its System pane.
async function systemPane(page: Page): Promise<Locator> {
  await page.locator(".menubar__app").click();
  await page.keyboard.press("Alt+Space");
  const input = page.getByLabel("Spotlight search");
  if (!(await input.isVisible())) await page.keyboard.press("Control+Space");
  await input.fill("System Settings");
  await page.locator(".spot__row").filter({ hasText: "System Settings" }).first().click();
  const win = page.locator('.window[aria-label="System Settings"]');
  await win.locator(".set__navitem", { hasText: "System" }).click();
  await expect(win.locator(".set__title", { hasText: "System" })).toBeVisible();
  return win;
}

test("the Realm can't be switched while Code is open: the popup lists its unsaved files and Shows the window", async ({ page }) => {
  await boot(page);
  const win = await openProject(page);
  await autoSaveOff(win);
  await openFile(win, "main.go");
  await typeAtEnd(page, win, "// unsaved");
  await expect.poll(() => aosd(page, (m) => Object.values(m.dirty).flat())).toEqual(["/home/aos/project/main.go"]);

  // The Desktop's own Code window is caught at once, before the warning.
  const settings = await systemPane(page);
  const toggle = settings.getByRole("switch", { name: "Start in Root Mode" });
  await toggle.click();
  const notice = page.getByTestId("rootmode-blocked");
  await expect(notice).toContainText("Code is open. Please save your files and close Code before switching mode.");
  await expect(notice.getByRole("list", { name: "Open in Code" })).toContainText("/home/aos/project1 unsaved");
  await expect(notice.getByRole("list", { name: "Unsaved files" })).toHaveText("main.go");
  await expect(page.getByRole("dialog", { name: "Turn on Root Mode" })).toHaveCount(0);
  await expect(toggle).toHaveAttribute("aria-checked", "false");

  // Show brings the Code window forward.
  await notice.getByRole("button", { name: "Show /home/aos/project" }).click();
  await expect(win).toHaveClass(/window--focused/);
  // Back to System Settings, which Code now covers, as a click on it would.
  await settings.dispatchEvent("pointerdown");
  await expect(settings).toHaveClass(/window--focused/);
  await notice.getByRole("button", { name: "OK" }).click();

  // Restart AOS is not blocked, but says what it would lose.
  await settings.getByRole("button", { name: "Restart AOS" }).click();
  const restart = page.getByRole("dialog", { name: "Restart AOS" });
  await expect(restart.getByRole("note", { name: "Unsaved in Code" })).toContainText("/home/aos/project/main.go");
  await restart.getByRole("button", { name: "Cancel" }).click();

  // Closed without saving, Code is out of the way here; but aosd knows of a
  // Workspace in another tab, which only its answer can name.
  await win.locator(".traffic__close").click();
  await page.getByRole("alertdialog", { name: "Save the changes to main.go?" }).getByRole("button", { name: "Don’t Save" }).click();
  await expect(win).toHaveCount(0);
  await aosd(page, (m) => {
    m.workspaces.ws_other = { root: "/srv/app", sub: "sub_other", open: true };
    m.dirty.ws_other = ["/srv/app/main.go"];
  });
  const turnOn = async () => {
    await toggle.click();
    const warn = page.getByRole("dialog", { name: "Turn on Root Mode" });
    await warn.getByText("I understand these actions cannot be undone").click();
    await warn.getByRole("button", { name: "Continue" }).click();
    const pw = page.getByRole("dialog", { name: "Enter your password" });
    await pw.getByLabel("Password").fill("secret");
    await pw.getByRole("button", { name: "Turn on Root Mode" }).click();
  };
  await turnOn();
  await expect(notice).toContainText("Code is open.");
  await expect(notice.getByRole("list", { name: "Open in Code" })).toContainText("/srv/app1 unsaved · another tab");
  await expect(notice.getByRole("list", { name: "Unsaved files" })).toHaveText("main.go");
  await expect(notice.getByRole("button", { name: /^Show/ })).toHaveCount(0);
  await notice.getByRole("button", { name: "OK" }).click();
  expect(await aosd(page, (m) => m.rootSwitches)).toEqual([]);

  // Once that closes too, the switch goes ahead.
  await aosd(page, (m) => (m.workspaces.ws_other.open = false));
  await turnOn();
  await expect(page.getByTestId("rootmode-working")).toBeVisible();
  expect(await aosd(page, (m) => m.rootSwitches)).toEqual([true]);
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
