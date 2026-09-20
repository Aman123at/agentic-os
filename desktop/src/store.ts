// The Desktop's client state (PLAN.md §4.3): the boot phase, the live connection
// to aosd's event stream, the theme, and the window manager. Server-derived
// state arrives over the event stream, so several tabs converge. The window
// layout is kept per tab (sessionStorage, so a reload restores this tab's own
// windows) and saved on the server (debounced) to seed new tabs.
import { create as createMessage } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "zustand";

import type { DownloadProgress, Event, InfoResponse, Notification } from "./gen/aos/v1/services_pb";
import { NotificationSchema } from "./gen/aos/v1/services_pb";
import type { Approval, ReplayStatus, Task, TaskStep } from "./gen/aos/v1/types_pb";
import { ApprovalDecision, Autonomy, TaskState, ToolCallStatus } from "./gen/aos/v1/types_pb";
import * as authSession from "./api/auth";
import { approvals as approvalApi, settings, system, tasks as taskApi, trash as trashApi } from "./api/client";
import { friendlyError } from "./api/error";
import * as session from "./api/session";
import { subscribe, type ConnState } from "./api/events";
import { appForFile } from "./apps/filetypes";
import { APPS, preloadApps, type AppId } from "./apps/registry";
import { defaultShortcuts, type ShortcutMap } from "./shell/shortcuts";
import { type WallpaperDesignId } from "./shell/wallpaper";
import { applyGlass, applyTheme, type ThemePref } from "./theme";

export type Phase = "loading" | "needs-signin" | "needs-password" | "ready" | "error";

// The Desktop refuses a password shorter than this, matching auth.MinPasswordLength
// on the server (ADR-0007). Refused, not warned (PLAN.md §18 M6.5).
export const MIN_PASSWORD_LENGTH = 12;

// The Desktop wallpaper: the drawn Aurora art, none (the plain gradient base),
// or one of the generated designs tuned by a hue and a saturation. The string
// literals are kept so every already-saved layout loads untouched.
export type GeneratedWallpaper = { design: WallpaperDesignId; hue: number; sat: number };
export type WallpaperPref = "aurora" | "none" | GeneratedWallpaper;

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
  // The Desktop appearance and keyboard remap, saved so every tab shares them
  // (PLAN.md §4.3). Absent in layouts saved before M4.5, so both are optional.
  wallpaper?: WallpaperPref;
  shortcuts?: Partial<ShortcutMap>;
  // Liquid Glass (M4.6), off unless saved on. Absent in older layouts.
  glass?: boolean;
  // Layouts saved before M4.2 kept the open Task here, for the old Tasks window.
  openTask?: string;
}

interface DesktopState {
  phase: Phase;
  error: string;
  // The message shown on the sign-in and change-password screens: a wrong
  // password, a weak one, a Machine that is unreachable.
  authError: string;
  // The expiry modal is over the desktop: the session lapsed, the windows are
  // kept, and signing in again resumes without a reload (PLAN.md §18 M6.5).
  expired: boolean;
  info?: InfoResponse;
  // Whether aosd is running in the Root Realm, learned before sign-in from the
  // public /realm probe so the login card can name the Realm (M7.10). Once
  // signed in, info.rootMode is the authoritative source.
  rootRealm: boolean;
  conn: ConnState;
  theme: ThemePref;
  wallpaper: WallpaperPref;
  // Liquid Glass on (M4.6); the frame watchdog turns it off if frames drop.
  glass: boolean;
  // The keyboard remap in force, read by shell/keyboard.ts. Seeded from the
  // defaults for the user's own computer, then overridden by what System Settings saved.
  shortcuts: ShortcutMap;
  windows: Win[];
  focused: string;
  notifications: Notification[];
  topZ: number;
  // How many items are in the Trash, so the Dock's tile can show a full bin
  // (PLAN.md M4.8 item 8.19). Counted at boot and after every change the
  // Desktop makes; the Trash has no event of its own.
  trashCount: number;

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

  // The progress of re-applying the Install Ledger at startup (PLAN.md §11),
  // shown in the menu bar while it runs and in Software's Replay view. Seeded
  // from Info at boot, then kept current by ReplayProgress events.
  replay?: ReplayStatus;
  // Bumped on every ServiceChanged event, so Activity Monitor's Services view
  // re-fetches without the store holding the whole Services list.
  serviceEpoch: number;
  // An Agent Session for the Terminal to Watch, set by Activity Monitor's Agents
  // tab; the Terminal consumes and clears it.
  watchSession: string;

  boot: () => Promise<void>;
  /** Signs in from the login screen; on success loads the shell, or shows the forced change. */
  signIn: (username: string, password: string) => Promise<void>;
  /** Sets a new password from the forced-change screen, then loads the shell. */
  submitPassword: (newPassword: string) => Promise<void>;
  /** Signs in again from the expiry modal and resumes the running desktop, no reload. */
  resumeSession: (username: string, password: string) => Promise<void>;
  /** Signs out: revokes the session, closes its streams, and returns to the login screen. */
  logout: () => Promise<void>;
  setTheme: (pref: ThemePref) => void;
  setWallpaper: (pref: WallpaperPref) => void;
  /** Turns Liquid Glass on or off; shared with every tab through the saved layout. */
  setGlass: (on: boolean) => void;
  /** Adds a client-only notification (e.g. the watchdog's), kept until dismissed here. */
  pushLocalNotification: (n: { title: string; body?: string }) => void;
  /** Updates the cached Info after the API key changes, so panes that re-mount re-seed truthfully. */
  setApiKeyInfo: (hint: string, source: string) => void;
  /** Remaps one keyboard shortcut; shared with every tab through the saved layout. */
  setShortcut: (action: keyof ShortcutMap, combo: string) => void;
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
  /** Sets the Trash count from a listing the caller already has. */
  setTrashCount: (n: number) => void;
  /** Counts the Trash again, after a change to it. */
  refreshTrashCount: () => Promise<void>;

  createTask: (prompt: string, autonomy?: Autonomy) => Promise<void>;
  /** Removes a finished Task and everything under it; the Audit Log keeps its record. */
  deleteTask: (id: string) => Promise<void>;
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
  /** Opens the Terminal and asks it to Watch an Agent Session read-only. */
  watchInTerminal: (sessionId: string) => void;
  clearWatchSession: () => void;
}

let nextId = 1;
// Ids for client-only notifications (the watchdog's), namespaced so they never
// collide with the server's and so dismissal can tell them apart.
let localNoteId = 1;
const desktopSize = () => ({ w: window.innerWidth, h: window.innerHeight });

// topmost is the window the user would call "the front one": the highest z among
// those actually on screen. Closing or minimizing the front window hands focus
// to it (PLAN.md M4.8 item 8.8).
function topmost(windows: Win[]): Win | undefined {
  return windows.filter((w) => !w.minimized).reduce<Win | undefined>((best, w) => (!best || w.z > best.z ? w : best), undefined);
}

// loadShell brings up the Desktop once the session is good: Info, the saved
// layout, the Agent surface already in flight, then the event stream. Shared by
// the first boot, a fresh sign-in and the forced first change, so all three end
// in the same ready shell.
async function loadShell(set: SetState, get: () => DesktopState): Promise<void> {
  const info = await system.info({});
  // A reload restores this tab's own layout; a new tab starts from the one last
  // saved on the server.
  let layout = readLocal();
  if (!layout) layout = (await settings.getDesktopState({}).catch(() => ({ state: "" }))).state;
  restore(layout, set);
  if (layout) writeLocal(layout);
  window.addEventListener("pagehide", () => flushSave(get));
  // Seed the Agent surface: Tasks already running and Approvals already waiting
  // when the Desktop loads (a later tab, or a reload).
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
  set({ phase: "ready", info, replay: info.replay, expired: false });
  startStream(set);
  preloadApps(info);
  void get().refreshTrashCount();
}

export const useDesktop = create<DesktopState>((set, get) => ({
  phase: "loading",
  error: "",
  authError: "",
  expired: false,
  rootRealm: false,
  conn: "connecting",
  theme: "auto",
  wallpaper: "aurora",
  glass: false,
  shortcuts: defaultShortcuts(),
  windows: [],
  focused: "",
  trashCount: 0,
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
  serviceEpoch: 0,
  watchSession: "",

  boot: async () => {
    // The Realm is public (M7.10): the login card names it before anyone signs
    // in. A failure here is not fatal — the card simply omits the line.
    try {
      const resp = await fetch(`${window.location.origin}/realm`);
      if (resp.ok) {
        const body = (await resp.json()) as { rootMode?: boolean };
        set({ rootRealm: !!body.rootMode });
      }
    } catch {
      // Offline or an old aosd without /realm: leave rootRealm false.
    }
    // Raise the expiry modal instead of bouncing to the login screen whenever a
    // live session lapses (PLAN.md §18 M6.5). Registered once, on first boot.
    session.onExpired(() => {
      if (get().phase !== "ready") return;
      authSession.stopProactiveRefresh();
      closeStream();
      set({ expired: true, authError: "", conn: "offline" });
    });
    try {
      if (!session.hasSession()) {
        set({ phase: "needs-signin" });
        return;
      }
      // A returning tab rotates its stored refresh token rather than ask for the
      // password again.
      const { mustChange } = await authSession.resume();
      authSession.startProactiveRefresh();
      if (mustChange) {
        set({ phase: "needs-password" });
        return;
      }
      await loadShell(set, get);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        // The stored token is dead: forget it and ask for the password.
        session.clearTokens();
        set({ phase: "needs-signin" });
        return;
      }
      // No session at all reads as "sign in", not an error card.
      if (!session.hasSession()) {
        set({ phase: "needs-signin" });
        return;
      }
      set({ phase: "error", error: friendlyError(err) });
    }
  },

  signIn: async (username, password) => {
    set({ authError: "" });
    try {
      const { mustChange } = await authSession.signIn(username, password);
      authSession.startProactiveRefresh();
      if (mustChange) {
        set({ phase: "needs-password" });
        return;
      }
      await loadShell(set, get);
    } catch (err) {
      set({ authError: friendlyError(err) });
    }
  },

  submitPassword: async (newPassword) => {
    if (newPassword.length < MIN_PASSWORD_LENGTH) {
      set({ authError: `The password must be at least ${MIN_PASSWORD_LENGTH} characters.` });
      return;
    }
    set({ authError: "" });
    try {
      await authSession.changePassword(newPassword);
      await loadShell(set, get);
    } catch (err) {
      set({ authError: friendlyError(err) });
    }
  },

  resumeSession: async (username, password) => {
    set({ authError: "" });
    try {
      await authSession.signIn(username, password);
      authSession.startProactiveRefresh();
      // The windows are untouched, so re-auth resumes in place: reconnect the
      // event stream and drop the modal without a reload.
      startStream(set);
      set({ expired: false });
    } catch (err) {
      set({ authError: friendlyError(err) });
    }
  },

  logout: async () => {
    authSession.stopProactiveRefresh();
    closeStream();
    await authSession.signOut();
    // Leaving the shell unmounts the Terminal and Browser, closing their
    // WebSockets; clear the surface so a later sign-in starts clean.
    set({
      phase: "needs-signin",
      authError: "",
      expired: false,
      windows: [],
      focused: "",
      tasks: {},
      approvals: {},
      steps: [],
      downloads: {},
      openTask: "",
      conn: "connecting",
    });
  },

  setTheme: (pref) => {
    applyTheme(pref);
    set({ theme: pref });
    save(get);
  },

  setWallpaper: (pref) => {
    set({ wallpaper: pref });
    save(get);
  },

  setGlass: (on) => {
    applyGlass(on);
    set({ glass: on });
    save(get);
  },

  pushLocalNotification: (n) => {
    // A local id so dismissNotification removes it here without a server call.
    const note = createMessage(NotificationSchema, { id: `local-${localNoteId++}`, title: n.title, body: n.body ?? "" });
    set((s) => ({ notifications: [note, ...s.notifications].slice(0, MAX_NOTIFICATIONS) }));
  },

  setApiKeyInfo: (hint, source) => {
    // Info is a boot-time snapshot the event stream never refreshes; patch it here
    // so the API key pane, which seeds from Info on mount, still shows the key
    // after the pane unmounts and re-mounts (a Settings tab switch).
    set((s) => (s.info ? { info: { ...s.info, apiKeyHint: hint, apiKeySource: source, apiKey: hint ? "present" : "missing" } } : {}));
  },

  setShortcut: (action, combo) => {
    set((s) => ({ shortcuts: { ...s.shortcuts, [action]: combo } }));
    save(get);
  },

  openApp: (appId, doc) => {
    const app = APPS[appId];
    if (!app) return;
    // Singleton apps focus their existing window instead of opening another, and
    // so does a document that is already open.
    const mine = get().windows.filter((w) => w.appId === appId && (!doc || w.state?.path === doc));
    const open = mine.at(-1);
    if (open && (app.singleton || doc)) {
      get().focusWindow(open.id);
      return;
    }
    // The Dock brings this app's windows back before it makes new ones, so a
    // minimized window is never stranded (there is no Mission Control here).
    const hidden = mine.filter((w) => w.minimized);
    if (hidden.length > 0) {
      const ids = new Set(hidden.map((w) => w.id));
      set((s) => ({ windows: s.windows.map((w) => (ids.has(w.id) ? { ...w, minimized: false } : w)) }));
      get().focusWindow(hidden[hidden.length - 1].id);
      return;
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
    set((s) => {
      const rest = s.windows.filter((w) => w.id !== id);
      return { windows: rest, focused: s.focused === id ? (topmost(rest)?.id ?? "") : s.focused };
    });
    save(get);
  },

  focusWindow: (id) => {
    set((s) => {
      const win = s.windows.find((w) => w.id === id);
      if (!win) return s;
      if (s.focused === id && !win.minimized && s.windows.at(-1)?.id === id) return s;
      const z = s.topZ + 1;
      // Focusing a minimized window restores it: that is what the Dock and ⌥`
      // do to bring one back.
      return { windows: s.windows.map((w) => (w.id === id ? { ...w, z, minimized: false } : w)), focused: id, topZ: z };
    });
    save(get);
  },

  setTrashCount: (n) => {
    if (get().trashCount !== n) set({ trashCount: n });
  },

  refreshTrashCount: async () => {
    const items = await trashApi.listTrash({}).catch(() => null);
    if (items) get().setTrashCount(items.items.length);
  },

  setRect: (id, rect) => {
    set((s) => ({ windows: s.windows.map((w) => (w.id === id ? { ...w, rect } : w)) }));
    save(get);
  },

  minimize: (id) => {
    set((s) => {
      const windows = s.windows.map((w) => (w.id === id ? { ...w, minimized: !w.minimized } : w));
      const hidden = windows.find((w) => w.id === id)?.minimized;
      // Minimizing the front window hands focus to whatever is now in front, so
      // the menu bar never falls back to "Agentic OS" with a window on screen
      // (PLAN.md M4.8 item 8.8).
      if (!hidden || s.focused !== id) return { windows };
      return { windows, focused: topmost(windows)?.id ?? "" };
    });
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

  createTask: async (prompt, autonomy) => {
    const text = prompt.trim();
    if (!text) return;
    // interactive: someone (this Desktop) is here to answer Approvals and
    // questions, so the Agent may ask rather than deny (PLAN.md §7). autonomy
    // is left Unspecified when the caller gives none, so the configured
    // Autonomy applies, exactly as `aos run` without --autonomy.
    const resp = await taskApi.createTask({ prompt: text, interactive: true, autonomy: autonomy ?? Autonomy.UNSPECIFIED });
    const task = resp.task;
    if (!task) return;
    set((s) => ({ tasks: { ...s.tasks, [task.id]: task } }));
    get().openTaskView(task.id);
  },

  deleteTask: async (id) => {
    await taskApi.deleteTask({ id });
    // Drop it here at once; the TaskChanged{removed} event confirms in every
    // tab. Also clear the open Task if it was the one removed.
    set((s) => {
      if (!s.tasks[id]) return s.openTask === id ? { openTask: "", steps: [] } : {};
      const tasks = { ...s.tasks };
      delete tasks[id];
      return s.openTask === id ? { tasks, openTask: "", steps: [] } : { tasks };
    });
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

  toggleSpotlight: (open) => {
    if (open !== false) void APPS.agent.preload().catch(() => {});
    set((s) => ({ spotlight: open ?? !s.spotlight, notifCenter: false }));
  },
  toggleNotifCenter: (open) => set((s) => ({ notifCenter: open ?? !s.notifCenter, spotlight: false })),
  dismissNotification: (id) => {
    // Client-only notifications live only in this tab, so drop them here.
    if (id?.startsWith("local-")) {
      set((s) => ({ notifications: s.notifications.filter((n) => n.id !== id) }));
      return;
    }
    // Clearing all also clears any local ones the server won't know about.
    if (!id) set((s) => ({ notifications: s.notifications.filter((n) => !n.id.startsWith("local-")) }));
    void system.dismissNotification(id ? { id } : { all: true }).catch(() => {});
  },

  revealInFinder: (dir, select) => {
    set({ finderJump: { dir, select }, spotlight: false });
    get().openApp("finder");
  },
  clearFinderJump: () => set({ finderJump: null }),

  watchInTerminal: (sessionId) => {
    set({ watchSession: sessionId });
    get().openApp("terminal");
  },
  clearWatchSession: () => set({ watchSession: "" }),
}));

// ---------------------------------------------------------------- event stream

// The live event stream's unsubscribe, kept so logout and an expiry can close it
// (the WebSockets are closed by unmounting the shell). A new stream replaces it.
let streamUnsub: (() => void) | undefined;

function closeStream() {
  streamUnsub?.();
  streamUnsub = undefined;
}

// startStream applies aosd's events, batched to one store commit per animation
// frame (PLAN.md §4.3 rule 4) so a burst never causes a render storm.
function startStream(set: SetState) {
  closeStream();
  let queue: Event[] = [];
  let scheduled = false;
  const flush = () => {
    scheduled = false;
    const batch = queue;
    queue = [];
    set((s) => reduce(s, batch));
    // An Agent's open_in_desktop shows a folder in the Finder, and opens a file
    // in the app for its type. An Agent starting to use the Browser opens its
    // window, so the user watches (PLAN.md M5.3).
    for (const e of batch) {
      // Another tab changed the Machine's preferences.
      if (e.kind?.case === "desktopState") {
        const { state, origin } = e.kind.value;
        if (origin !== TAB_ID) adoptPreferences(state, useDesktop.getState(), set);
        continue;
      }
      if (e.kind?.case !== "openInDesktop") continue;
      const { path, dir, app } = e.kind.value;
      if (app) {
        if (app === "browser" && useDesktop.getState().info?.browser) useDesktop.getState().openApp("browser");
        continue;
      }
      if (dir) useDesktop.getState().revealInFinder(path, "");
      else useDesktop.getState().openFile(path);
    }
  };
  streamUnsub = subscribe({
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
  let replay = s.replay;
  let serviceEpoch = s.serviceEpoch;
  let openTask = s.openTask;
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
        // A deleted Task leaves every tab: drop it and end its downloads. The
        // event carries only the id.
        if (k.value.removed) {
          if (t && tasks[t.id]) {
            tasks = { ...tasks };
            delete tasks[t.id];
          }
          if (t && openTask === t.id) {
            openTask = "";
            steps = [];
          }
          if (t) dropDownloads((d) => d.taskId !== t.id);
          break;
        }
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
      case "replayProgress": {
        if (k.value.status) replay = k.value.status;
        break;
      }
      // The Services view re-fetches when a Service changes; the store keeps only
      // a counter, not the list.
      case "serviceChanged": {
        serviceEpoch++;
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
  if (openTask !== s.openTask) out.openTask = openTask;
  if (replay !== s.replay) out.replay = replay;
  if (serviceEpoch !== s.serviceEpoch) out.serviceEpoch = serviceEpoch;
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

// This tab's id. It rides along with every save so the DesktopStateChanged event
// it causes can be told apart from another tab's (PLAN.md §4.3).
const TAB_ID = Math.random().toString(36).slice(2);

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
    wallpaper: s.wallpaper,
    glass: s.glass,
    shortcuts: s.shortcuts,
    focused: s.focused,
    windows: s.windows.map(({ id, appId, title, rect, minimized, maximized, state }) => ({ id, appId, title, rect, minimized, maximized, state })),
  };
  const json = JSON.stringify(state);
  writeLocal(json);
  void settings.saveDesktopState({ state: json, origin: TAB_ID }).catch(() => {});
}

// adoptPreferences applies what another tab just saved. The preferences are the
// Machine's, so every tab follows them; the window layout is the tab's own
// (PLAN.md §4.3), so the windows in the event are ignored and this tab's local
// copy is rewritten with its own.
function adoptPreferences(json: string, s: DesktopState, set: SetState): void {
  let saved: Persisted;
  try {
    saved = JSON.parse(json) as Persisted;
  } catch {
    return;
  }
  const theme = saved.theme ?? "auto";
  const wallpaper = saved.wallpaper ?? "aurora";
  const glass = saved.glass ?? false;
  const shortcuts = { ...defaultShortcuts(), ...saved.shortcuts };
  const same =
    theme === s.theme &&
    // The wallpaper can be an object now, so two structurally-equal values are
    // never ===; compare by value, as with shortcuts. Without this every
    // DesktopStateChanged from another tab needlessly re-sets state.
    JSON.stringify(wallpaper) === JSON.stringify(s.wallpaper) &&
    glass === s.glass &&
    JSON.stringify(shortcuts) === JSON.stringify(s.shortcuts);
  if (same) return;
  applyTheme(theme);
  applyGlass(glass);
  set({ theme, wallpaper, glass, shortcuts });
  // Keep this tab's own copy current without telling the server again, which
  // would bounce the event back and forth between the tabs.
  writeLocal(
    JSON.stringify({ theme, wallpaper, glass, shortcuts, focused: s.focused, windows: s.windows.map(({ id, appId, title, rect, minimized, maximized, state }) => ({ id, appId, title, rect, minimized, maximized, state })) } satisfies Persisted),
  );
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
  applyGlass(saved.glass ?? false);
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
  // A saved remap may cover only some actions (or come from an older layout);
  // the defaults for the user's own computer fill in the rest.
  const shortcuts = { ...defaultShortcuts(), ...saved.shortcuts };
  set({ theme: saved.theme ?? "auto", wallpaper: saved.wallpaper ?? "aurora", glass: saved.glass ?? false, shortcuts, windows, focused, topZ: windows.length + 1 });
}

// upgradeWindow turns the M3 Tasks window of an older saved layout into the
// Agent app, keeping the Task it showed.
function upgradeWindow(w: Persisted["windows"][number], openTask: string): Persisted["windows"][number] {
  if ((w.appId as string) !== "tasks") return w;
  return { ...w, appId: "agent", title: APPS.agent.name, state: { view: "tasks", task: openTask } };
}
