// The Desktop's client state (PLAN.md §4.3): the boot phase, the live connection
// to aosd's event stream, the theme, and the window manager. Server-derived
// state arrives over the event stream, so several tabs converge. The window
// layout is kept per tab (sessionStorage, so a reload restores this tab's own
// windows) and saved on the server (debounced) to seed new tabs.
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "zustand";

import type { DownloadProgress, Event, InfoResponse, Notification } from "./gen/aos/v1/services_pb";
import type { Approval, Task, TaskStep } from "./gen/aos/v1/types_pb";
import { ApprovalDecision, TaskState, ToolCallStatus } from "./gen/aos/v1/types_pb";
import { approvals as approvalApi, auth, settings, system, tasks as taskApi } from "./api/client";
import { subscribe, type ConnState } from "./api/events";
import { appForFile } from "./apps/filetypes";
import { APPS, type AppId } from "./apps/registry";
import { applyTheme, type ThemePref } from "./theme";

export type Phase = "loading" | "needs-signin" | "ready" | "error";

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Win {
  id: string;
  appId: AppId;
  title: string;
  rect: Rect;
  z: number;
  minimized: boolean;
  maximized: boolean;
  restore?: Rect; // the rect to return to when un-maximizing
  state?: Record<string, string>; // the app's own state, kept with the window (e.g. Finder's folder)
}

interface Persisted {
  theme: ThemePref;
  windows: Array<Pick<Win, "id" | "appId" | "title" | "rect" | "minimized" | "maximized" | "state">>;
  focused: string;
  // Layouts saved before M4.2 kept the open Task here, for the old Tasks window.
  openTask?: string;
}

interface DesktopState {
  phase: Phase;
  error: string;
  info?: InfoResponse;
  conn: ConnState;
  theme: ThemePref;
  windows: Win[];
  focused: string;
  notifications: Notification[];
  topZ: number;

  // The Agent surface (PLAN.md §4.3): live Tasks, pending Approvals and the
  // step feed of the one Task the Agent app shows. Which Task that is lives in
  // the Agent window's own state; openTask follows it.
  tasks: Record<string, Task>;
  approvals: Record<string, Approval>; // pending only
  openTask: string;
  steps: TaskStep[];
  // Downloads still in progress, by the Tool call's step id, for the Dock's
  // Downloads stack.
  downloads: Record<string, DownloadProgress>;
  spotlight: boolean;
  notifCenter: boolean;
  // A file for the Finder to reveal, set by Spotlight; the Finder consumes and
  // clears it. `dir` is the Finder's own logical folder (e.g. "~"), `select` the
  // entry's absolute path.
  finderJump: { dir: string; select: string } | null;

  boot: () => Promise<void>;
  setTheme: (pref: ThemePref) => void;
  /** Opens an app; given a document, opens it in its own window, or focuses the window already showing it. */
  openApp: (appId: AppId, doc?: string) => void;
  /** Opens a file in the app for its type, or shows it in the Finder when none opens it. */
  openFile: (path: string) => void;
  closeWindow: (id: string) => void;
  focusWindow: (id: string) => void;
  setRect: (id: string, rect: Rect) => void;
  minimize: (id: string) => void;
  toggleMaximize: (id: string) => void;
  setWinState: (id: string, patch: Record<string, string>) => void;

  createTask: (prompt: string) => Promise<void>;
  /** Opens the Agent app at a Task. */
  openTaskView: (id: string) => void;
  /** Makes a Task the one whose step feed is live; the Agent app calls it. */
  selectTask: (id: string) => void;
  loadTask: (id: string) => Promise<void>;
  /** Loads more of the Task history than boot does, for the Agent app's list. */
  loadTasks: (limit: number) => Promise<void>;
  decideApproval: (id: string, decision: ApprovalDecision) => Promise<void>;
  answerQuestion: (id: string, text: string) => Promise<void>;
  sendFollowUp: (id: string, text: string) => Promise<void>;
  cancelTask: (id: string) => Promise<void>;
  resumeTask: (id: string) => Promise<void>;
  stopAll: () => Promise<void>;
  toggleSpotlight: (open?: boolean) => void;
  toggleNotifCenter: (open?: boolean) => void;
  /** Dismisses one notification, or every one without an id; the event updates every tab. */
  dismissNotification: (id?: string) => void;
  revealInFinder: (dir: string, select: string) => void;
  clearFinderJump: () => void;
}

let nextId = 1;
const desktopSize = () => ({ w: window.innerWidth, h: window.innerHeight });

// signIn exchanges a one-time code from the URL hash (#code=…) for the session
// cookie, then removes it from the address bar (PLAN.md §7.6).
async function signIn(): Promise<void> {
  const hash = new URLSearchParams(window.location.hash.slice(1));
  const code = hash.get("code");
  if (!code) return;
  await auth.exchangeLoginCode({ code });
  history.replaceState(null, "", window.location.pathname + window.location.search);
}

export const useDesktop = create<DesktopState>((set, get) => ({
  phase: "loading",
  error: "",
  conn: "connecting",
  theme: "auto",
  windows: [],
  focused: "",
  notifications: [],
  topZ: 1,
  tasks: {},
  approvals: {},
  openTask: "",
  steps: [],
  downloads: {},
  spotlight: false,
  notifCenter: false,
  finderJump: null,

  boot: async () => {
    try {
      await signIn();
      const info = await system.info({});
      // A reload restores this tab's own layout; a new tab starts from the one
      // last saved on the server.
      let layout = readLocal();
      if (!layout) layout = (await settings.getDesktopState({}).catch(() => ({ state: "" }))).state;
      restore(layout, set);
      if (layout) writeLocal(layout);
      window.addEventListener("pagehide", () => flushSave(get));
      // Seed the Agent surface: Tasks already running and Approvals already
      // waiting when the Desktop loads (a later tab, or a reload).
      const [taskList, pending, kept] = await Promise.all([
        taskApi.listTasks({ limit: 50 }).catch(() => ({ tasks: [] })),
        approvalApi.listPending({}).catch(() => ({ approvals: [] })),
        system.listNotifications({}).catch(() => ({ notifications: [] })),
      ]);
      set({
        tasks: Object.fromEntries(taskList.tasks.map((t) => [t.id, t])),
        approvals: Object.fromEntries(pending.approvals.map((a) => [a.id, a])),
        notifications: kept.notifications.slice(0, MAX_NOTIFICATIONS),
      });
      set({ phase: "ready", info });
      startStream(set);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        set({ phase: "needs-signin" });
        return;
      }
      set({ phase: "error", error: ConnectError.from(err).message });
    }
  },

  setTheme: (pref) => {
    applyTheme(pref);
    set({ theme: pref });
    save(get);
  },

  openApp: (appId, doc) => {
    const app = APPS[appId];
    if (!app) return;
    // Singleton apps focus their existing window instead of opening another,
    // and so does a document that is already open.
    if (app.singleton || doc) {
      const open = get().windows.find((w) => w.appId === appId && (!doc || w.state?.path === doc));
      if (open) {
        get().focusWindow(open.id);
        set((s) => ({ windows: s.windows.map((w) => (w.id === open.id ? { ...w, minimized: false } : w)) }));
        return;
      }
    }
    const { w: dw, h: dh } = desktopSize();
    const n = get().windows.length;
    const rect: Rect = {
      x: Math.min(120 + n * 28, Math.max(40, dw - app.size.w - 40)),
      y: Math.min(80 + n * 28, Math.max(40, dh - app.size.h - 40)),
      w: app.size.w,
      h: app.size.h,
    };
    const id = `win-${nextId++}`;
    const z = get().topZ + 1;
    // A document window is titled with the file's name, as on macOS.
    const title = doc ? doc.slice(doc.lastIndexOf("/") + 1) : app.name;
    const state = doc ? { path: doc } : undefined;
    set((s) => ({ windows: [...s.windows, { id, appId, title, rect, z, minimized: false, maximized: false, state }], focused: id, topZ: z }));
    save(get);
  },

  openFile: (path) => {
    const app = appForFile(path);
    set({ spotlight: false });
    if (app) get().openApp(app, path);
    else get().revealInFinder(path.slice(0, path.lastIndexOf("/")) || "/", path);
  },

  closeWindow: (id) => {
    const win = get().windows.find((w) => w.id === id);
    if (win?.state?.edited === "1" && !window.confirm(`Close ${win.title} without saving your changes?`)) return;
    set((s) => ({ windows: s.windows.filter((w) => w.id !== id) }));
    save(get);
  },

  focusWindow: (id) => {
    set((s) => {
      if (s.focused === id && s.windows.at(-1)?.id === id) return s;
      const z = s.topZ + 1;
      return { windows: s.windows.map((w) => (w.id === id ? { ...w, z } : w)), focused: id, topZ: z };
    });
  },

  setRect: (id, rect) => {
    set((s) => ({ windows: s.windows.map((w) => (w.id === id ? { ...w, rect } : w)) }));
    save(get);
  },

  minimize: (id) => {
    set((s) => ({ windows: s.windows.map((w) => (w.id === id ? { ...w, minimized: !w.minimized } : w)) }));
    save(get);
  },

  toggleMaximize: (id) => {
    set((s) => ({
      windows: s.windows.map((w) => {
        if (w.id !== id) return w;
        if (w.maximized) return { ...w, maximized: false, rect: w.restore ?? w.rect };
        const { w: dw, h: dh } = desktopSize();
        return { ...w, maximized: true, restore: w.rect, rect: { x: 8, y: 36, w: dw - 16, h: dh - 44 } };
      }),
    }));
    save(get);
  },

  setWinState: (id, patch) => {
    const win = get().windows.find((w) => w.id === id);
    if (!win || Object.entries(patch).every(([k, v]) => win.state?.[k] === v)) return;
    set((s) => ({ windows: s.windows.map((w) => (w.id === id ? { ...w, state: { ...w.state, ...patch } } : w)) }));
    save(get);
  },

  // ------------------------------------------------------------- Agent surface

  createTask: async (prompt) => {
    const text = prompt.trim();
    if (!text) return;
    // interactive: someone (this Desktop) is here to answer Approvals and
    // questions, so the Agent may ask rather than deny (PLAN.md §7).
    const resp = await taskApi.createTask({ prompt: text, interactive: true });
    const task = resp.task;
    if (!task) return;
    set((s) => ({ tasks: { ...s.tasks, [task.id]: task } }));
    get().openTaskView(task.id);
  },

  openTaskView: (id) => {
    set({ notifCenter: false, spotlight: false });
    get().openApp("agent");
    const win = get().windows.find((w) => w.appId === "agent");
    // Saved with the window, so the Task comes back with the layout.
    if (win) get().setWinState(win.id, { view: "tasks", task: id });
    get().selectTask(id);
  },

  selectTask: (id) => {
    if (get().openTask === id) return;
    set({ openTask: id, steps: [] });
    if (id) void get().loadTask(id);
  },

  loadTasks: async (limit) => {
    const resp = await taskApi.listTasks({ limit }).catch(() => null);
    if (!resp) return;
    // Events may have brought newer copies of some Tasks meanwhile; keep those.
    set((s) => ({ tasks: { ...Object.fromEntries(resp.tasks.map((t) => [t.id, t])), ...s.tasks } }));
  },

  loadTask: async (id) => {
    const resp = await taskApi.getTask({ id }).catch(() => null);
    if (!resp?.task) return;
    const loaded = resp.task;
    // Events may have overtaken this reply: a fast Task can finish while
    // GetTask is in flight, and then no later event would correct an older
    // snapshot. So the newer copy of the Task and of each step wins.
    set((s) => {
      const approvals = { ...s.approvals };
      for (const a of resp.approvals) {
        if (a.decision === ApprovalDecision.UNSPECIFIED) approvals[a.id] = a;
        else delete approvals[a.id];
      }
      const current = s.tasks[id];
      return {
        tasks: current && newerTask(current, loaded) === current ? s.tasks : { ...s.tasks, [id]: loaded },
        approvals,
        // Only the open Task keeps a live step feed.
        steps: s.openTask === id ? mergeSteps(resp.steps, s.steps) : s.steps,
      };
    });
  },

  decideApproval: async (id, decision) => {
    // Drop it from the pending set at once; the ApprovalChanged event confirms.
    set((s) => {
      if (!s.approvals[id]) return {};
      const approvals = { ...s.approvals };
      delete approvals[id];
      return { approvals };
    });
    await approvalApi.decide({ approvalId: id, decision });
  },

  answerQuestion: async (id, text) => {
    await taskApi.answerQuestion({ id, text });
    void get().loadTask(id);
  },

  sendFollowUp: async (id, text) => {
    const resp = await taskApi.sendFollowUp({ id, text, interactive: true });
    if (resp.task) set((s) => ({ tasks: { ...s.tasks, [id]: resp.task! } }));
  },

  cancelTask: async (id) => {
    await taskApi.cancelTask({ id });
  },

  resumeTask: async (id) => {
    const resp = await taskApi.resumeTask({ id, interactive: true });
    if (resp.task) set((s) => ({ tasks: { ...s.tasks, [id]: resp.task! } }));
  },

  stopAll: async () => {
    await taskApi.stopAll({});
  },

  toggleSpotlight: (open) => set((s) => ({ spotlight: open ?? !s.spotlight, notifCenter: false })),
  toggleNotifCenter: (open) => set((s) => ({ notifCenter: open ?? !s.notifCenter, spotlight: false })),
  dismissNotification: (id) => {
    void system.dismissNotification(id ? { id } : { all: true }).catch(() => {});
  },

  revealInFinder: (dir, select) => {
    set({ finderJump: { dir, select }, spotlight: false });
    get().openApp("finder");
  },
  clearFinderJump: () => set({ finderJump: null }),
}));

// ---------------------------------------------------------------- event stream

// startStream applies aosd's events, batched to one store commit per animation
// frame (PLAN.md §4.3 rule 4) so a burst never causes a render storm.
function startStream(set: SetState) {
  let queue: Event[] = [];
  let scheduled = false;
  const flush = () => {
    scheduled = false;
    const batch = queue;
    queue = [];
    set((s) => reduce(s, batch));
    // An Agent's open_in_desktop shows a folder in the Finder, and opens a file
    // in the app for its type.
    for (const e of batch) {
      if (e.kind?.case !== "openInDesktop") continue;
      const { path, dir } = e.kind.value;
      if (dir) useDesktop.getState().revealInFinder(path, "");
      else useDesktop.getState().openFile(path);
    }
  };
  subscribe({
    onState: (conn) => set({ conn }),
    onEvent: (event) => {
      queue.push(event);
      if (!scheduled) {
        scheduled = true;
        requestAnimationFrame(flush);
      }
    },
  });
}

// Notifications stay until dismissed; the store holds the newest of them.
const MAX_NOTIFICATIONS = 50;

function reduce(s: DesktopState, batch: Event[]): Partial<DesktopState> {
  let notifications = s.notifications;
  let tasks = s.tasks;
  let approvals = s.approvals;
  let steps = s.steps;
  let downloads = s.downloads;
  const dropDownloads = (keep: (d: DownloadProgress, stepId: string) => boolean) => {
    const next = Object.fromEntries(Object.entries(downloads).filter(([id, d]) => keep(d, id)));
    if (Object.keys(next).length !== Object.keys(downloads).length) downloads = next;
  };

  for (const e of batch) {
    const k = e.kind;
    switch (k?.case) {
      case "notification": {
        const n = k.value;
        const others = notifications.filter((o) => o.id !== n.id);
        notifications = n.dismissed ? others : [n, ...others].slice(0, MAX_NOTIFICATIONS);
        break;
      }
      case "taskChanged": {
        const t = k.value.task;
        if (t) tasks = { ...tasks, [t.id]: t };
        // A Task that stops ends its downloads, whatever their last progress said.
        if (t && t.state !== TaskState.RUNNING) dropDownloads((d) => d.taskId !== t.id);
        break;
      }
      case "approval": {
        const a = k.value.approval;
        if (!a) break;
        if (a.decision === ApprovalDecision.UNSPECIFIED) {
          approvals = { ...approvals, [a.id]: a };
        } else if (approvals[a.id]) {
          approvals = { ...approvals };
          delete approvals[a.id];
        }
        break;
      }
      // The step feed is kept only for the Task the Agent app shows.
      // TaskStepChanged replaces a step wholesale; TextDelta appends streamed
      // text to it — so the two never double-count.
      case "taskStep": {
        const step = k.value.step;
        if (step && step.taskId === s.openTask) steps = upsertStep(steps, step);
        // A download is over when its Tool call is.
        if (step && downloads[step.id] && (step.toolCall?.status ?? 0) >= ToolCallStatus.SUCCEEDED) dropDownloads((_, id) => id !== step.id);
        break;
      }
      case "downloadProgress": {
        const d = k.value;
        downloads = { ...downloads, [d.stepId]: d };
        break;
      }
      case "textDelta": {
        const d = k.value;
        if (d.taskId === s.openTask) steps = appendDelta(steps, d.stepId, d.delta);
        break;
      }
    }
  }

  const out: Partial<DesktopState> = {};
  if (notifications !== s.notifications) out.notifications = notifications;
  if (tasks !== s.tasks) out.tasks = tasks;
  if (approvals !== s.approvals) out.approvals = approvals;
  if (steps !== s.steps) out.steps = steps;
  if (downloads !== s.downloads) out.downloads = downloads;
  return out;
}

// newerTask picks the later of two copies of a Task: by update time, then by
// usage, which grows without changing the update time.
function newerTask(a: Task, b: Task): Task {
  const at = millisOf(a.updatedAt);
  const bt = millisOf(b.updatedAt);
  if (at !== bt) return at > bt ? a : b;
  return tokensOf(b) > tokensOf(a) ? b : a;
}

function millisOf(ts?: { seconds: bigint; nanos: number }): number {
  return ts ? Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6) : 0;
}

function tokensOf(t: Task): number {
  return t.usage ? Number(t.usage.inputTokens) + Number(t.usage.outputTokens) : 0;
}

// mergeSteps combines a loaded step feed with the steps events brought, keeping
// the further-along copy of each step, in step order.
function mergeSteps(loaded: TaskStep[], live: TaskStep[]): TaskStep[] {
  const byId = new Map(loaded.map((st) => [st.id, st]));
  for (const st of live) {
    const other = byId.get(st.id);
    if (!other || stepProgress(st) >= stepProgress(other)) byId.set(st.id, st);
  }
  return [...byId.values()].sort((a, b) => Number(a.seq - b.seq));
}

// stepProgress orders copies of one step: a tool call moves from pending to a
// final status, and streamed text only grows.
function stepProgress(st: TaskStep): number {
  const status = st.toolCall?.status ?? ToolCallStatus.UNSPECIFIED;
  const rank = status >= ToolCallStatus.SUCCEEDED ? 3 : status === ToolCallStatus.RUNNING ? 2 : status === ToolCallStatus.UNSPECIFIED ? 0 : 1;
  return rank * 1e9 + st.text.length + (st.toolCall?.result.length ?? 0);
}

function upsertStep(steps: TaskStep[], step: TaskStep): TaskStep[] {
  const i = steps.findIndex((x) => x.id === step.id);
  if (i < 0) return [...steps, step];
  const next = steps.slice();
  next[i] = step;
  return next;
}

function appendDelta(steps: TaskStep[], stepId: string, delta: string): TaskStep[] {
  const i = steps.findIndex((x) => x.id === stepId);
  if (i < 0) return steps;
  const next = steps.slice();
  next[i] = { ...next[i], text: next[i].text + delta };
  return next;
}

// ---------------------------------------------------------------- persistence

type SetState = (partial: Partial<DesktopState> | ((s: DesktopState) => Partial<DesktopState>)) => void;

const LAYOUT_KEY = "aos.layout";
let saveTimer: ReturnType<typeof setTimeout> | undefined;

// save writes the layout, debounced so a drag does not spam it.
function save(get: () => DesktopState) {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => flushSave(get), 400);
}

// flushSave writes a pending layout now: to this tab (so its reload restores it)
// and to the server (so a new tab starts from it). It also runs on pagehide, so
// a reload straight after a change keeps the change.
function flushSave(get: () => DesktopState) {
  if (saveTimer === undefined) return;
  clearTimeout(saveTimer);
  saveTimer = undefined;
  const s = get();
  const state: Persisted = {
    theme: s.theme,
    focused: s.focused,
    windows: s.windows.map(({ id, appId, title, rect, minimized, maximized, state }) => ({ id, appId, title, rect, minimized, maximized, state })),
  };
  const json = JSON.stringify(state);
  writeLocal(json);
  void settings.saveDesktopState({ state: json }).catch(() => {});
}

// This tab's own copy of the layout. Storage can be unavailable (a private
// window, blocked site data); the server copy still restores then.
function readLocal(): string {
  try {
    return sessionStorage.getItem(LAYOUT_KEY) ?? "";
  } catch {
    return "";
  }
}

function writeLocal(json: string) {
  try {
    sessionStorage.setItem(LAYOUT_KEY, json);
  } catch {
    // Nothing to do: the server copy is the fallback.
  }
}

function restore(json: string, set: SetState) {
  if (!json) return;
  let saved: Persisted;
  try {
    saved = JSON.parse(json) as Persisted;
  } catch {
    return;
  }
  applyTheme(saved.theme ?? "auto");
  // Re-key the windows so restored ids never collide with freshly opened ones.
  let focused = "";
  const windows: Win[] = (saved.windows ?? [])
    .map((w) => upgradeWindow(w, saved.openTask ?? ""))
    .filter((w) => APPS[w.appId])
    .map((w, i) => {
      const id = `win-${nextId++}`;
      if (w.id === saved.focused) focused = id;
      return { ...w, id, z: i + 1, restore: undefined };
    });
  set({ theme: saved.theme ?? "auto", windows, focused, topZ: windows.length + 1 });
}

// upgradeWindow turns the M3 Tasks window of an older saved layout into the
// Agent app, keeping the Task it showed.
function upgradeWindow(w: Persisted["windows"][number], openTask: string): Persisted["windows"][number] {
  if ((w.appId as string) !== "tasks") return w;
  return { ...w, appId: "agent", title: APPS.agent.name, state: { view: "tasks", task: openTask } };
}
