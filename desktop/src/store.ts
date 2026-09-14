// The Desktop's client state (PLAN.md §4.3): the boot phase, the live connection
// to aosd's event stream, the theme, and the window manager. Server-derived
// state arrives over the event stream, so several tabs converge; the window
// layout is saved on the server (debounced) and restored on reload.
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "zustand";

import type { Event, InfoResponse, Notification } from "./gen/aos/v1/services_pb";
import type { Approval, Task, TaskStep } from "./gen/aos/v1/types_pb";
import { ApprovalDecision } from "./gen/aos/v1/types_pb";
import { approvals as approvalApi, auth, settings, system, tasks as taskApi } from "./api/client";
import { subscribe, type ConnState } from "./api/events";
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
}

interface Persisted {
  theme: ThemePref;
  windows: Array<Pick<Win, "id" | "appId" | "title" | "rect" | "minimized" | "maximized">>;
  focused: string;
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

  // The Agent surface (PLAN.md §4.3, M3.4): live Tasks, pending Approvals and
  // the step feed of the one Task currently open in the Tasks app.
  tasks: Record<string, Task>;
  approvals: Record<string, Approval>; // pending only
  openTask: string;
  steps: TaskStep[];
  spotlight: boolean;
  notifCenter: boolean;

  boot: () => Promise<void>;
  setTheme: (pref: ThemePref) => void;
  openApp: (appId: AppId) => void;
  closeWindow: (id: string) => void;
  focusWindow: (id: string) => void;
  setRect: (id: string, rect: Rect) => void;
  minimize: (id: string) => void;
  toggleMaximize: (id: string) => void;

  createTask: (prompt: string) => Promise<void>;
  openTaskView: (id: string) => void;
  loadTask: (id: string) => Promise<void>;
  decideApproval: (id: string, decision: ApprovalDecision) => Promise<void>;
  answerQuestion: (id: string, text: string) => Promise<void>;
  sendFollowUp: (id: string, text: string) => Promise<void>;
  cancelTask: (id: string) => Promise<void>;
  resumeTask: (id: string) => Promise<void>;
  stopAll: () => Promise<void>;
  toggleSpotlight: (open?: boolean) => void;
  toggleNotifCenter: (open?: boolean) => void;
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
  spotlight: false,
  notifCenter: false,

  boot: async () => {
    try {
      await signIn();
      const info = await system.info({});
      const saved = await settings.getDesktopState({}).catch(() => ({ state: "" }));
      restore(saved.state, set);
      // Seed the Agent surface: Tasks already running and Approvals already
      // waiting when the Desktop loads (a later tab, or a reload).
      const [taskList, pending] = await Promise.all([
        taskApi.listTasks({ limit: 50 }).catch(() => ({ tasks: [] })),
        approvalApi.listPending({}).catch(() => ({ approvals: [] })),
      ]);
      set({
        tasks: Object.fromEntries(taskList.tasks.map((t) => [t.id, t])),
        approvals: Object.fromEntries(pending.approvals.map((a) => [a.id, a])),
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

  openApp: (appId) => {
    const app = APPS[appId];
    if (!app) return;
    // Singleton apps focus their existing window instead of opening another.
    if (app.singleton) {
      const open = get().windows.find((w) => w.appId === appId);
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
    set((s) => ({ windows: [...s.windows, { id, appId, title: app.name, rect, z, minimized: false, maximized: false }], focused: id, topZ: z }));
    save(get);
  },

  closeWindow: (id) => {
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
    set({ openTask: id, steps: [], notifCenter: false, spotlight: false });
    get().openApp("tasks");
    void get().loadTask(id);
  },

  loadTask: async (id) => {
    const resp = await taskApi.getTask({ id }).catch(() => null);
    if (!resp?.task) return;
    const task = resp.task;
    set((s) => {
      const approvals = { ...s.approvals };
      for (const a of resp.approvals) {
        if (a.decision === ApprovalDecision.UNSPECIFIED) approvals[a.id] = a;
        else delete approvals[a.id];
      }
      return {
        tasks: { ...s.tasks, [task.id]: task },
        approvals,
        // Only the open Task keeps a live step feed.
        steps: s.openTask === id ? resp.steps : s.steps,
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

function reduce(s: DesktopState, batch: Event[]): Partial<DesktopState> {
  let notifications = s.notifications;
  let tasks = s.tasks;
  let approvals = s.approvals;
  let steps = s.steps;

  for (const e of batch) {
    const k = e.kind;
    switch (k?.case) {
      case "notification":
        notifications = [k.value, ...notifications].slice(0, 50);
        break;
      case "taskChanged": {
        const t = k.value.task;
        if (t) tasks = { ...tasks, [t.id]: t };
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
      // The step feed is kept only for the Task open in the Tasks app.
      // TaskStepChanged replaces a step wholesale; TextDelta appends streamed
      // text to it — so the two never double-count.
      case "taskStep": {
        const step = k.value.step;
        if (step && step.taskId === s.openTask) steps = upsertStep(steps, step);
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
  return out;
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

let saveTimer: ReturnType<typeof setTimeout> | undefined;

// save writes the layout to the server, debounced so a drag does not spam it.
function save(get: () => DesktopState) {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    const s = get();
    const state: Persisted = {
      theme: s.theme,
      focused: s.focused,
      windows: s.windows.map(({ id, appId, title, rect, minimized, maximized }) => ({ id, appId, title, rect, minimized, maximized })),
    };
    void settings.saveDesktopState({ state: JSON.stringify(state) }).catch(() => {});
  }, 400);
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
    .filter((w) => APPS[w.appId])
    .map((w, i) => {
      const id = `win-${nextId++}`;
      if (w.id === saved.focused) focused = id;
      return { ...w, id, z: i + 1, restore: undefined };
    });
  set({ theme: saved.theme ?? "auto", windows, focused, topZ: windows.length + 1 });
}
