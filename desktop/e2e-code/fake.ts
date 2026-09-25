// A fake aosd for Code's specs (PLAN.md M8.3–M8.5), in the page itself: an
// init script replaces fetch for aosd's routes with an in-memory Machine — a
// file tree, the event stream and CodeService — so the Desktop runs from a Vite
// dev server with no Docker. Unlike e2e-auth's route fakes it can hold the event
// stream open and push WorkspaceChanged events, which Code's live reloads need.
//
// A spec drives the Machine through window.__aosd (see FakeMachine): writing a
// file there is a change "made by something else", and the Workspace's watch
// reports it like inotify would.
import type { Page } from "@playwright/test";

export interface FakeOptions {
  rootMode?: boolean;
  // Files to start with, by absolute path; a path ending in "/" is a folder.
  files?: Record<string, string>;
  // Paths the Realm's user may read but not write (M8.4). In Standard Mode
  // anything outside the home folder is read-only too, as it is for aos.
  readOnly?: string[];
  // The search engine Find in Files reports (M8.7); ripgrep unless told.
  engine?: string;
  // A Machine without universal-ctags (M8.8).
  noCtags?: boolean;
}

// FakeMachine is what a spec reaches through page.evaluate(() => window.__aosd).
export interface FakeMachine {
  write(path: string, text: string): void;
  remove(path: string): void;
  read(path: string): string | null;
  calls: string[];
  // The subscription ids handed out, newest last, and a way to drop the newest
  // stream as a network blip would.
  subscriptions: string[];
  dropStream(): void;
  dirty: Record<string, string[]>;
  workspaces: Record<string, { root: string; sub: string; open: boolean }>;
  // Confirmed changes to Sensitive System Files, and the versions they
  // replaced (M8.5).
  sensitiveWrites: string[];
  backups: Record<string, string[]>;
  // User Sessions (M8.6): where each started, and whether it is still open.
  sessions: Record<string, { cwd: string; open: boolean }>;
  // The Agent (M8.11): what the next run of a Task does, taken by each
  // CreateTask or SendFollowUp; and what the Desktop asked for.
  agent: AgentAct[];
  taskRequests: { method: string; body: Record<string, unknown> }[];
}

// AgentAct is one thing the fake Agent does in a run: change a file with a
// Files Tool (keeping its before-image, as aosd does), change one with a
// command (keeping none), delete one, or say something.
export type AgentAct = { write: string; text: string } | { command: string; text: string } | { remove: string } | { say: string };

export async function installFakeAosd(page: Page, opts: FakeOptions = {}): Promise<void> {
  await page.addInitScript(() => {
    try {
      // Terminals hand their xterm to the spec (term.ts).
      (window as unknown as { __AOS_E2E__: boolean }).__AOS_E2E__ = true;
      localStorage.setItem("aos.refresh", "refresh-token");
      // An empty Desktop to start with; a reload keeps the layout it saved.
      if (!sessionStorage.getItem("aos.layout")) sessionStorage.setItem("aos.layout", JSON.stringify({ theme: "light", windows: [], focused: "" }));
    } catch {
      // The spec fails loudly if the session is missing.
    }
  });
  await page.addInitScript(fake, opts);
}

// fake runs in the page. It must not reference anything outside itself.
function fake(opts: FakeOptions) {
  const HOME = "/home/aos";
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  type Node = { dir: boolean; data: Uint8Array; mtime: number };
  const fs = new Map<string, Node>();
  let clock = Date.parse("2026-09-24T10:00:00Z");
  const tick = () => (clock += 7);
  const mkdirp = (p: string) => {
    for (let d = p; d && d !== "/"; d = parent(d)) if (!fs.has(d)) fs.set(d, { dir: true, data: new Uint8Array(), mtime: tick() });
  };
  const parent = (p: string) => (p.lastIndexOf("/") <= 0 ? "/" : p.slice(0, p.lastIndexOf("/")));
  const base = (p: string) => p.slice(p.lastIndexOf("/") + 1);
  fs.set("/", { dir: true, data: new Uint8Array(), mtime: tick() });
  mkdirp(HOME);
  for (const [p, text] of Object.entries(opts.files ?? {})) {
    if (p.endsWith("/")) mkdirp(p.slice(0, -1));
    else {
      mkdirp(parent(p));
      fs.set(p, { dir: false, data: enc.encode(text), mtime: tick() });
    }
  }

  const readOnly = new Set(opts.readOnly ?? []);
  // writable is what access(W_OK) says as the Realm's user; in Root Mode AOS's
  // own state is never writable (M8.4).
  const writable = (p: string) =>
    !readOnly.has(p) && (opts.rootMode ? !p.startsWith("/var/lib/aos") && !p.startsWith("/etc/aos") : p === HOME || p.startsWith(HOME + "/"));

  // ------------------------------------------------------------ the Machine
  type Sub = { id: string; push: (msg: unknown) => void; end: () => void };
  const subs: Sub[] = [];
  const workspaces: FakeMachine["workspaces"] = {};
  const machine: FakeMachine = {
    calls: [],
    subscriptions: [],
    dirty: {},
    workspaces,
    sensitiveWrites: [],
    backups: {},
    // A spec carries Sessions over a reload, as aosd keeps them running.
    sessions: JSON.parse(sessionStorage.getItem("fake.sessions") ?? "{}") as FakeMachine["sessions"],
    agent: [],
    taskRequests: [],
    write(p, text) {
      const existed = fs.has(p);
      mkdirp(parent(p));
      fs.set(p, { dir: false, data: enc.encode(text), mtime: tick() });
      changed(existed ? "KIND_CHANGED" : "KIND_CREATED", p, false);
    },
    remove(p) {
      const node = fs.get(p);
      for (const k of [...fs.keys()]) if (k === p || k.startsWith(p + "/")) fs.delete(k);
      if (node) changed("KIND_DELETED", p, node.dir);
    },
    read(p) {
      const n = fs.get(p);
      return n && !n.dir ? dec.decode(n.data) : null;
    },
    dropStream() {
      subs.at(-1)?.end();
    },
  };
  (window as unknown as { __aosd: FakeMachine }).__aosd = machine;

  // changed tells each Workspace holding p, a little later, as a watch would.
  function changed(kind: string, p: string, dir: boolean) {
    setTimeout(() => {
      for (const [id, w] of Object.entries(workspaces)) {
        if (!w.open || !(p === w.root || p.startsWith(w.root + "/"))) continue;
        subs.find((s) => s.id === w.sub)?.push({ event: { time: new Date().toISOString(), workspace: { workspaceId: id, changes: [{ kind, path: p, dir }] } } });
      }
    }, 30);
  }

  // The fake's Sensitive System Files, in Root Mode only, as policy.Sensitive.
  const sensitive = (p: string): string => {
    if (!opts.rootMode) return "";
    if (p === "/etc/sudoers" || p.startsWith("/etc/sudoers.d/")) return "A mistake here can lock everyone out of sudo.";
    if (p === "/etc/fstab") return "A mistake here can stop the Machine from starting.";
    return "";
  };

  const info = (p: string) => {
    const n = fs.get(p)!;
    return {
      path: p,
      name: base(p) || "/",
      dir: n.dir,
      size: String(n.data.length),
      mode: n.dir ? 0o40000755 : 0o644,
      modifiedAt: new Date(n.mtime).toISOString(),
      writable: writable(p),
      sensitiveReason: sensitive(p),
    };
  };

  const abs = (p: string) => {
    if (p === "~") return HOME;
    if (p.startsWith("~/")) return HOME + p.slice(1);
    if (!p.startsWith("/")) return HOME + "/" + p;
    return p.replace(/\/+$/, "") || "/";
  };

  class RpcError {
    constructor(
      public code: string,
      public message: string,
      public details?: unknown[],
    ) {}
  }
  const notFound = (p: string) => new RpcError("not_found", `lstat ${p}: no such file or directory`);

  const b64 = (bytes: Uint8Array) => {
    let s = "";
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s);
  };
  const unb64 = (s: string) => Uint8Array.from(atob(s || ""), (c) => c.charCodeAt(0));

  // A SensitivePath detail, encoded by hand: path = 1, reason = 2.
  const sensitiveDetail = (p: string, reason: string) => {
    const field = (tag: number, text: string) => {
      const b = enc.encode(text);
      const len: number[] = [];
      let n = b.length;
      do {
        len.push((n & 0x7f) | (n > 0x7f ? 0x80 : 0));
        n >>= 7;
      } while (n > 0);
      return [tag, ...len, ...b];
    };
    return { type: "aos.v1.SensitivePath", value: b64(new Uint8Array([...field(0x0a, p), ...field(0x12, reason)])).replace(/=+$/, "") };
  };
  // guard refuses an unconfirmed change to a Sensitive System File, as aosd does.
  const guard = (p: string, body: Record<string, unknown>): boolean => {
    const why = sensitive(p);
    if (why && !body.confirmSensitive) throw new RpcError("failed_precondition", `${p} is a Sensitive System File. ${why} Confirm to change it`, [sensitiveDetail(p, why)]);
    if (why) machine.sensitiveWrites.push(p);
    return Boolean(why);
  };

  // ------------------------------------------------------------ the Agent (M8.11)
  type FTask = {
    task: Record<string, unknown>;
    steps: Record<string, unknown>[];
    // base is what Change Review compares the file with (M8.12): the
    // before-image with the hunks kept; null for "does not exist".
    changes: { change: Record<string, unknown>; before: Uint8Array | null; base: Uint8Array | null; reviewed: boolean }[];
  };
  const ftasks = new Map<string, FTask>();
  let taskCount = 0;
  let stepCount = 0;
  const broadcast = (event: Record<string, unknown>) => {
    for (const s of subs) s.push({ event: { time: new Date().toISOString(), ...event } });
  };
  const setTaskState = (id: string, state: string, summary?: string) => {
    const t = ftasks.get(id);
    if (!t) return;
    t.task = { ...t.task, state, updatedAt: new Date(tick()).toISOString(), ...(summary !== undefined ? { summary } : {}) };
    broadcast({ taskChanged: { task: t.task } });
  };
  const addStep = (id: string, step: Record<string, unknown>) => {
    const t = ftasks.get(id);
    if (!t) return;
    const full = { id: `s_${++stepCount}`, taskId: id, seq: String(t.steps.length + 1), createdAt: new Date(tick()).toISOString(), ...step };
    t.steps.push(full);
    broadcast({ taskStep: { step: full } });
  };
  const toolStep = (id: string, tool: string, args: Record<string, unknown>, result: string) =>
    addStep(id, { kind: "STEP_KIND_TOOL_CALL", text: "", toolCall: { callId: `c_${stepCount}`, tool, argumentsJson: JSON.stringify(args), status: "TOOL_CALL_STATUS_SUCCEEDED", result } });
  // keepBefore keeps a file's before-image the first time the Task changes it.
  const keepBefore = (t: FTask, p: string, tool: string) => {
    if (t.changes.some((c) => c.change.path === p)) return;
    const n = fs.get(p);
    const before = n && !n.dir ? n.data.slice() : null;
    t.changes.push({
      change: { path: p, tool, existed: Boolean(n), folder: Boolean(n?.dir), size: String(n?.data.length ?? 0), kept: Boolean(n && !n.dir), whole: false, changedAt: new Date(clock).toISOString() },
      before,
      base: before,
      reviewed: false,
    });
  };
  const changeOf = (taskId: string, path: string) => {
    const c = ftasks.get(taskId)?.changes.find((x) => x.change.path === abs(path));
    if (!c) throw new RpcError("not_found", `the Task did not change ${path}`);
    return c;
  };
  // changeNow is a change as ListChanges tells it: whether the file on disk
  // still differs from its base.
  const changeNow = (c: FTask["changes"][number]) => {
    const n = fs.get(c.change.path as string);
    const now = n && !n.dir ? n.data : null;
    const same = (a: Uint8Array | null, b: Uint8Array | null) => (a === null || b === null ? a === b : a.length === b.length && a.every((x, i) => x === b[i]));
    return { ...c.change, pending: c.change.folder ? !c.reviewed : !same(now, c.base), reviewed: c.reviewed, baseExists: c.base !== null };
  };
  const pause = () => new Promise((r) => setTimeout(r, 40));
  async function runTask(id: string) {
    const acts = machine.agent.splice(0);
    await pause();
    setTaskState(id, "TASK_STATE_RUNNING");
    let said = "";
    for (const act of acts) {
      await pause();
      const t = ftasks.get(id);
      if (!t) return;
      if ("write" in act) {
        const p = abs(act.write);
        const tool = fs.has(p) ? "edit_file" : "write_file";
        keepBefore(t, p, tool);
        machine.write(p, act.text);
        toolStep(id, tool, { path: p }, `Wrote ${p}.`);
      } else if ("command" in act) {
        machine.write(abs(act.command), act.text);
        toolStep(id, "run_command", { command: `generate ${act.command}` }, "exit 0");
      } else if ("remove" in act) {
        const p = abs(act.remove);
        keepBefore(t, p, "delete");
        machine.remove(p);
        toolStep(id, "delete", { path: p }, `Moved ${p} to the Trash.`);
      } else {
        said = act.say;
        addStep(id, { kind: "STEP_KIND_AGENT_TEXT", text: act.say });
      }
    }
    await pause();
    setTaskState(id, "TASK_STATE_SUCCEEDED", said);
  }

  let subCount = 0;
  let wsCount = 0;
  let sessionCount = Object.keys(machine.sessions).length;

  function unary(method: string, body: Record<string, unknown>): unknown {
    const str = (k: string) => String(body[k] ?? "");
    switch (method) {
      case "AuthService/Refresh":
        return { accessToken: "access", refreshToken: "refresh-token" };
      case "SystemService/Info":
        return { rootMode: Boolean(opts.rootMode), version: "fake" };
      case "TaskService/ListTasks": {
        const ws = str("workspace");
        return { tasks: [...ftasks.values()].map((t) => t.task).filter((t) => !ws || t.workspace === ws).reverse() };
      }
      case "TaskService/CreateTask": {
        machine.taskRequests.push({ method: "CreateTask", body });
        const editor = body.editor as { workspace?: string } | undefined;
        const id = `t_${++taskCount}`;
        const at = new Date(tick()).toISOString();
        const task = { id, title: str("prompt").slice(0, 60), prompt: str("prompt"), state: "TASK_STATE_QUEUED", autonomy: "AUTONOMY_CONFIRM_RISKY", interactive: true, model: "gpt-fake", usage: { costKnown: true }, createdAt: at, updatedAt: at, workspace: editor?.workspace ? abs(editor.workspace) : "" };
        ftasks.set(id, { task, steps: [], changes: [] });
        addStep(id, { kind: "STEP_KIND_USER_MESSAGE", text: str("prompt") });
        void runTask(id);
        return { task };
      }
      case "TaskService/SendFollowUp": {
        machine.taskRequests.push({ method: "SendFollowUp", body });
        const t = ftasks.get(str("id"));
        if (!t) throw new RpcError("not_found", "no such Task");
        addStep(t.task.id as string, { kind: "STEP_KIND_USER_MESSAGE", text: str("text") });
        setTaskState(t.task.id as string, "TASK_STATE_QUEUED");
        void runTask(t.task.id as string);
        return { task: t.task };
      }
      case "TaskService/GetTask": {
        const t = ftasks.get(str("id"));
        if (!t) throw new RpcError("not_found", "no such Task");
        return { task: t.task, steps: t.steps, approvals: [] };
      }
      case "TaskService/DeleteTask": {
        const t = ftasks.get(str("id"));
        if (!t) throw new RpcError("not_found", "no such Task");
        ftasks.delete(str("id"));
        broadcast({ taskChanged: { task: { id: str("id") }, removed: true } });
        return {};
      }
      case "TaskService/ListChanges": {
        const t = ftasks.get(str("taskId"));
        if (!t) throw new RpcError("not_found", "no such Task");
        return { changes: t.changes.map(changeNow) };
      }
      case "TaskService/GetBefore": {
        const c = changeOf(str("taskId"), str("path"));
        return { change: changeNow(c), content: c.base ? b64(c.base) : "" };
      }
      case "TaskService/ReviewChange": {
        machine.taskRequests.push({ method: "ReviewChange", body });
        const c = changeOf(str("taskId"), str("path"));
        if (body.keepAll) {
          const n = fs.get(c.change.path as string);
          c.base = n && !n.dir ? n.data.slice() : null;
        } else c.base = body.gone ? null : unb64(str("base"));
        c.reviewed = true;
        return { change: changeNow(c) };
      }
      case "TaskService/DiscardChange": {
        machine.taskRequests.push({ method: "DiscardChange", body });
        const c = changeOf(str("taskId"), str("path"));
        const p = c.change.path as string;
        if (!c.base) {
          if (fs.has(p)) machine.remove(p);
        } else {
          guard(p, body);
          const existed = fs.has(p);
          fs.set(p, { dir: false, data: c.base.slice(), mtime: tick() });
          changed(existed ? "KIND_CHANGED" : "KIND_CREATED", p, false);
        }
        return { change: changeNow(c) };
      }
      case "ApprovalService/ListPending":
        return { approvals: [] };
      case "SystemService/ListNotifications":
        return { notifications: [] };
      case "SettingsService/GetDesktopState":
        return { state: "" };
      case "TrashService/ListTrash":
        return { items: [] };

      case "FileService/Stat": {
        const p = abs(str("path"));
        if (!fs.has(p)) throw notFound(p);
        return { info: info(p) };
      }
      case "FileService/List": {
        const p = abs(str("path"));
        if (!fs.get(p)?.dir) throw notFound(p);
        const kids = [...fs.keys()].filter((k) => k !== p && parent(k) === p);
        kids.sort((a, b) => Number(fs.get(b)!.dir) - Number(fs.get(a)!.dir) || base(a).localeCompare(base(b)));
        return { entries: kids.map(info) };
      }
      case "FileService/Read": {
        const p = abs(str("path"));
        const n = fs.get(p);
        if (!n || n.dir) throw notFound(p);
        const off = Number(body.offset ?? 0);
        const lim = Number(body.limit ?? 0) || 1 << 20;
        const part = n.data.slice(off, off + lim);
        return { content: b64(part), eof: off + part.length >= n.data.length };
      }
      case "FileService/Write": {
        const p = abs(str("path"));
        const exists = fs.has(p);
        if (exists && !body.overwrite) throw new RpcError("already_exists", `write ${p}: already exists`);
        if (!writable(p) || !writable(parent(p))) throw new RpcError("permission_denied", `open ${p}: permission denied`);
        if (guard(p, body) && exists) (machine.backups[p] ??= []).unshift(dec.decode(fs.get(p)!.data));
        mkdirp(parent(p));
        fs.set(p, { dir: false, data: unb64(str("content")), mtime: tick() });
        changed(exists ? "KIND_CHANGED" : "KIND_CREATED", p, false);
        return {};
      }
      case "FileService/MakeFolder": {
        const p = abs(str("path"));
        if (fs.has(p)) throw new RpcError("already_exists", `mkdir ${p}: already exists`);
        guard(p, body);
        mkdirp(p);
        changed("KIND_CREATED", p, true);
        return {};
      }
      case "FileService/Move": {
        const from = abs(str("source"));
        const to = abs(str("destination"));
        if (!fs.has(from)) throw notFound(from);
        if (fs.has(to)) throw new RpcError("already_exists", `rename ${to}: already exists`);
        if (!guard(from, body)) guard(to, body);
        for (const k of [...fs.keys()]) {
          if (k === from || k.startsWith(from + "/")) {
            fs.set(to + k.slice(from.length), fs.get(k)!);
            fs.delete(k);
          }
        }
        changed("KIND_DELETED", from, fs.get(to)!.dir);
        changed("KIND_CREATED", to, fs.get(to)!.dir);
        return {};
      }
      case "FileService/Delete": {
        const p = abs(str("path"));
        if (!fs.has(p)) throw notFound(p);
        guard(p, body);
        machine.remove(p);
        return { item: { id: p, name: base(p) } };
      }

      case "FileService/CheckSensitive": {
        const p = abs(str("path"));
        if (!sensitive(p) || p !== "/etc/sudoers") return {};
        const text = dec.decode(unb64(str("content")));
        return text.includes("ALL") ? { checker: "visudo -c", passed: true, output: "/etc/sudoers: parsed OK" } : { checker: "visudo -c", output: `/etc/sudoers:1:1: syntax error\n${text}` };
      }
      case "FileService/ListSensitiveBackups": {
        const p = abs(str("path"));
        return { backups: (machine.backups[p] ?? []).map((_, i) => ({ id: `v${i}`, savedAt: new Date(clock - i * 1000).toISOString(), size: "1" })) };
      }
      case "FileService/RestoreSensitiveBackup": {
        const p = abs(str("path"));
        const kept = machine.backups[p] ?? [];
        const old = kept[Number(str("id").slice(1))];
        if (old === undefined) throw new RpcError("not_found", "no such kept version");
        kept.unshift(dec.decode(fs.get(p)!.data));
        fs.set(p, { dir: false, data: enc.encode(old), mtime: tick() });
        changed("KIND_CHANGED", p, false);
        return {};
      }

      case "AuthService/CreateTicket":
        return { ticket: "ticket" };
      case "SessionService/CreateSession": {
        // The shell enters the folder if its user may, else stays home.
        const want = body.cwd ? abs(str("cwd")) : HOME;
        const cwd = fs.get(want)?.dir && (opts.rootMode || want === HOME || want.startsWith(HOME + "/")) ? want : HOME;
        const id = `u_${++sessionCount}`;
        machine.sessions[id] = { cwd, open: true };
        return { session: { id, cwd, createdAt: new Date(clock).toISOString() } };
      }
      case "SessionService/ListSessions":
        return { sessions: Object.entries(machine.sessions).filter(([, s]) => s.open).map(([id]) => ({ id })) };
      case "SessionService/CloseSession": {
        const s = machine.sessions[str("id")];
        if (s) s.open = false;
        return {};
      }

      case "CodeService/OpenWorkspace": {
        const sub = str("subscriptionId");
        if (!subs.some((s) => s.id === sub)) throw new RpcError("not_found", "the event subscription has ended; subscribe again");
        const root = abs(str("path"));
        if (!fs.has(root)) throw notFound(root);
        if (!fs.get(root)!.dir) throw new RpcError("invalid_argument", `${root} is not a folder`);
        const id = `ws_${++wsCount}`;
        workspaces[id] = { root, sub, open: true };
        // The watch's first word, sent before the reply as aosd may.
        subs.find((s) => s.id === sub)?.push({ event: { time: new Date().toISOString(), workspace: { workspaceId: id, watch: { state: "STATE_LIVE" } } } });
        return { id, root, rootMode: Boolean(opts.rootMode) };
      }
      case "CodeService/CloseWorkspace": {
        const w = workspaces[str("workspaceId")];
        if (!w?.open) throw new RpcError("not_found", "no such open Workspace");
        w.open = false;
        return {};
      }
      case "CodeService/FindFiles": {
        const w = workspaces[str("workspaceId")];
        if (!w?.open) throw new RpcError("not_found", "no such open Workspace");
        const q = str("query").toLowerCase().replace(/\s+/g, "");
        const all = [...fs.keys()].filter((k) => k.startsWith(w.root + "/") && !fs.get(k)!.dir).map((k) => k.slice(w.root.length + 1));
        // In order, the file's name first, like aosd's ranking; then shorter.
        const find = (rel: string, from: number) => {
          const pos: number[] = [];
          for (let i = from; i < rel.length && pos.length < q.length; i++) if (rel[i].toLowerCase() === q[pos.length]) pos.push(i);
          return pos.length === q.length ? pos : null;
        };
        const found = all
          .map((rel) => {
            const name = rel.lastIndexOf("/") + 1;
            const inName = find(rel, name);
            return { rel, pos: inName ?? find(rel, 0), inName: Boolean(inName) };
          })
          .filter((f) => f.pos)
          .sort((a, b) => Number(b.inName) - Number(a.inName) || a.rel.length - b.rel.length || a.rel.localeCompare(b.rel));
        return { files: found.slice(0, Number(body.limit) || 50).map((f) => ({ path: `${w.root}/${f.rel}`, positions: f.pos })), total: all.length };
      }
      case "CodeService/Definition": {
        const w = symbolWorkspace(str("workspaceId"));
        const from = str("path");
        const dir = parent(from);
        const named = allSymbols(w.root).filter((x) => x.name === str("name"));
        // Those in the asking file's language, when there are any, as aosd.
        const lang = from.slice(from.lastIndexOf(".") + 1);
        const found = named.some((x) => x.language === lang) ? named.filter((x) => x.language === lang) : named;
        found.sort((a, b) => Number(b.path === from) - Number(a.path === from) || Number(parent(b.path) === dir) - Number(parent(a.path) === dir) || a.path.localeCompare(b.path));
        return { symbols: found, index: { files: 1, total: 1 } };
      }
      case "CodeService/DocumentSymbols":
        symbolWorkspace(str("workspaceId"));
        return { symbols: symbolsOf(str("path")), index: { files: 1, total: 1 } };
      case "CodeService/WorkspaceSymbols": {
        const w = symbolWorkspace(str("workspaceId"));
        const q = str("query").toLowerCase();
        const inOrder = (name: string) => {
          let i = 0;
          for (const c of name.toLowerCase()) if (c === q[i]) i++;
          return i === q.length;
        };
        return { symbols: allSymbols(w.root).filter((x) => inOrder(x.name)).slice(0, Number(body.limit) || 100), index: { files: 1, total: 1 } };
      }
      case "CodeService/References": {
        const w = workspaces[str("workspaceId")];
        if (!w?.open) throw new RpcError("not_found", "no such open Workspace");
        const ext = str("path").slice(str("path").lastIndexOf("."));
        const res = searchText({ workspaceId: str("workspaceId"), query: `\\b${str("name")}\\b`, regex: true, caseSensitive: true });
        return { files: res.files.filter((f) => f.path.endsWith(ext)) };
      }
      case "CodeService/ReportDirty": {
        const id = str("workspaceId");
        if (!workspaces[id]?.open) throw new RpcError("not_found", "no such open Workspace");
        machine.dirty[id] = (body.paths as string[]) ?? [];
        return {};
      }
    }
    return {};
  }

  // The fake's ctags: definitions it can see by their keyword.
  const SYMBOL = /^\s*(?:export\s+)?(?:(func)\s+(?:\([^)]*\)\s*)?|(function)\s+|(class)\s+|(def)\s+|(type)\s+|(const)\s+)([A-Za-z_$][\w$]*)/;
  const symbolsOf = (p: string) => {
    const n = fs.get(p);
    if (!n || n.dir) return [];
    return dec
      .decode(n.data)
      .split("\n")
      .flatMap((text, i) => {
        const m = SYMBOL.exec(text);
        if (!m) return [];
        const kind = m[1] ?? m[2] ?? m[3] ?? (m[4] ? "function" : undefined) ?? m[5] ?? m[6];
        return [{ name: m[7], kind, path: p, line: i + 1, language: p.slice(p.lastIndexOf(".") + 1) }];
      });
  };
  const allSymbols = (root: string) => [...fs.keys()].filter((k) => k.startsWith(root + "/")).sort().flatMap(symbolsOf);
  const symbolWorkspace = (id: string) => {
    const w = workspaces[id];
    if (!w?.open) throw new RpcError("not_found", "no such open Workspace");
    if (opts.noCtags) throw new RpcError("failed_precondition", "universal-ctags is not installed on this Machine; Go to Definition and symbols need it: sudo apt install universal-ctags");
    return w;
  };

  // searchText is Find in Files over the fake's tree, one file per result.
  function searchText(req: Record<string, unknown>) {
    const w = workspaces[String(req.workspaceId)];
    if (!w?.open) throw new RpcError("not_found", "no such open Workspace");
    const q = String(req.query ?? "");
    let re: RegExp;
    try {
      re = new RegExp(req.regex ? q : q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), req.caseSensitive ? "g" : "gi");
    } catch (err) {
      throw new RpcError("invalid_argument", `the search is not valid: ${(err as Error).message}`);
    }
    const out: { path: string; matches: unknown[] }[] = [];
    let matches = 0;
    for (const k of [...fs.keys()].sort()) {
      const n = fs.get(k)!;
      if (n.dir || !k.startsWith(w.root + "/")) continue;
      const found: { line: number; text: string; ranges: { start: number; end: number }[] }[] = [];
      dec.decode(n.data).split("\n").forEach((line, i) => {
        const lead = line.length - line.trimStart().length;
        const text = line.trim();
        const ranges = [...line.matchAll(re)].filter((m) => m[0]).map((m) => ({ start: m.index! - lead, end: m.index! - lead + m[0].length }));
        if (ranges.length) found.push({ line: i + 1, text, ranges });
      });
      if (found.length) {
        out.push({ path: k, matches: found });
        matches += found.length;
      }
    }
    return { files: out, summary: { files: out.length, matches, engine: opts.engine ?? "ripgrep" } };
  }

  // A server stream: Connect's envelopes over a body the fake keeps open.
  function stream(method: string, raw: Uint8Array, signal?: AbortSignal | null): Response {
    let ctrl!: ReadableStreamDefaultController<Uint8Array>;
    let done = false;
    const frame = (flags: number, msg: unknown) => {
      const json = enc.encode(JSON.stringify(msg));
      const out = new Uint8Array(5 + json.length);
      out[0] = flags;
      new DataView(out.buffer).setUint32(1, json.length);
      out.set(json, 5);
      return out;
    };
    const body = new ReadableStream<Uint8Array>({ start: (c) => void (ctrl = c) });
    const push = (msg: unknown) => !done && ctrl.enqueue(frame(0, msg));
    const end = (error?: RpcError) => {
      if (done) return;
      done = true;
      ctrl.enqueue(frame(2, error ? { error: { code: error.code, message: error.message } } : {}));
      ctrl.close();
    };
    if (method === "EventService/Subscribe") {
      const sub: Sub = {
        id: `sub_${++subCount}`,
        push,
        end: () => {
          end();
          // aosd closes a subscription's Workspaces when it ends.
          for (const w of Object.values(workspaces)) if (w.sub === sub.id) w.open = false;
          subs.splice(subs.indexOf(sub), 1);
        },
      };
      subs.push(sub);
      machine.subscriptions.push(sub.id);
      push({ subscriptionId: sub.id });
      signal?.addEventListener("abort", () => sub.end());
    } else if (method === "CodeService/SearchText") {
      const req = JSON.parse(dec.decode(raw.slice(5))) as Record<string, unknown>;
      setTimeout(() => {
        try {
          const res = searchText(req);
          for (const f of res.files) push({ files: [f] });
          push({ summary: res.summary });
          end();
        } catch (err) {
          end(err instanceof RpcError ? err : new RpcError("internal", String(err)));
        }
      }, 10);
    } else if (method === "FileService/Watch") {
      // Finder's folder stream: the listing now, then held open.
      const req = JSON.parse(dec.decode(raw.slice(5))) as Record<string, unknown>;
      queueMicrotask(() => {
        try {
          push(unary("FileService/List", req));
        } catch {
          end();
        }
      });
    } else {
      end();
    }
    return new Response(body, { status: 200, headers: { "Content-Type": "application/connect+json" } });
  }

  // A Session's socket: a shell that knows pwd and cd, and echoes like a PTY.
  const RealWebSocket = window.WebSocket;
  class FakeSocket extends EventTarget {
    static readonly OPEN = 1;
    binaryType = "arraybuffer";
    readyState = 0;
    onopen: ((ev: Event) => void) | null = null;
    onmessage: ((ev: MessageEvent) => void) | null = null;
    onclose: ((ev: CloseEvent) => void) | null = null;
    onerror: ((ev: Event) => void) | null = null;
    private line = "";
    private id: string;
    constructor(url: string) {
      super();
      this.id = decodeURIComponent(new URL(url).pathname.split("/").pop()!);
      setTimeout(() => {
        const s = machine.sessions[this.id];
        if (!s?.open) return this.close();
        this.readyState = 1;
        this.onopen?.(new Event("open"));
        this.out(this.prompt());
      }, 5);
    }
    private prompt() {
      return opts.rootMode ? "root@machine:" + machine.sessions[this.id].cwd + "# " : "aos$ ";
    }
    private out(text: string) {
      this.onmessage?.(new MessageEvent("message", { data: enc.encode(text).buffer }));
    }
    send(data: string | ArrayBuffer | Uint8Array) {
      if (typeof data === "string") return; // a resize
      const s = machine.sessions[this.id];
      for (const ch of dec.decode(data as Uint8Array)) {
        if (ch !== "\r") {
          this.line += ch;
          this.out(ch);
          continue;
        }
        const [cmd, arg] = this.line.trim().split(/\s+/);
        this.line = "";
        let reply = "";
        if (cmd === "pwd") reply = s.cwd + "\r\n";
        else if (cmd === "cd") {
          const to = arg?.startsWith("/") ? arg : `${s.cwd}/${arg ?? ""}`;
          if (fs.get(to)?.dir) s.cwd = to;
          else reply = `bash: cd: ${arg}: No such file or directory\r\n`;
        } else if (cmd) reply = `bash: ${cmd}: command not found\r\n`;
        this.out("\r\n" + reply + this.prompt());
      }
    }
    close() {
      if (this.readyState === 3) return;
      this.readyState = 3;
      this.onclose?.(new CloseEvent("close"));
    }
  }
  window.WebSocket = function (url: string | URL, protocols?: string | string[]) {
    const u = String(url);
    return u.includes("/ws/session/") ? new FakeSocket(u) : new RealWebSocket(u, protocols);
  } as unknown as typeof WebSocket;
  Object.assign(window.WebSocket, { OPEN: 1, CLOSED: 3, CONNECTING: 0, CLOSING: 2 });

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
    if (url.pathname === "/realm") return new Response(JSON.stringify({ rootMode: Boolean(opts.rootMode) }), { headers: { "Content-Type": "application/json" } });
    const m = url.pathname.match(/^\/aos\.v1\.(\w+\/\w+)$/);
    if (!m) return realFetch(input, init);
    const method = m[1];
    machine.calls.push(method);
    const contentType = new Headers(init?.headers).get("Content-Type") ?? "";
    const raw = new Uint8Array(await new Response(init?.body ?? null).arrayBuffer());
    if (contentType.includes("connect+json")) return stream(method, raw, init?.signal);
    await new Promise((r) => setTimeout(r, 5)); // a round trip, so ordering is not free
    try {
      const out = unary(method, raw.length ? (JSON.parse(dec.decode(raw)) as Record<string, unknown>) : {});
      return new Response(JSON.stringify(out), { status: 200, headers: { "Content-Type": "application/json" } });
    } catch (err) {
      if (!(err instanceof RpcError)) throw err;
      const status = { not_found: 404, already_exists: 409, permission_denied: 403, invalid_argument: 400, failed_precondition: 400 }[err.code] ?? 500;
      return new Response(JSON.stringify({ code: err.code, message: err.message, details: err.details }), { status, headers: { "Content-Type": "application/json" } });
    }
  };
}
