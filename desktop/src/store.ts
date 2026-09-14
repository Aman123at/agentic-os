// The Desktop's client state (PLAN.md §4.3): the boot phase, the live connection
// to aosd's event stream, the theme, and the window manager. Server-derived
// state arrives over the event stream, so several tabs converge; the window
// layout is saved on the server (debounced) and restored on reload.
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "zustand";

import type { Event, InfoResponse, Notification } from "./gen/aos/v1/services_pb";
import { auth, settings, system } from "./api/client";
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

  boot: () => Promise<void>;
  setTheme: (pref: ThemePref) => void;
  openApp: (appId: AppId) => void;
  closeWindow: (id: string) => void;
  focusWindow: (id: string) => void;
  setRect: (id: string, rect: Rect) => void;
  minimize: (id: string) => void;
  toggleMaximize: (id: string) => void;
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

  boot: async () => {
    try {
      await signIn();
      const info = await system.info({});
      const saved = await settings.getDesktopState({}).catch(() => ({ state: "" }));
      restore(saved.state, set);
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
  for (const e of batch) {
    if (e.kind?.case === "notification") {
      notifications = [e.kind.value, ...notifications].slice(0, 50);
    }
  }
  return notifications === s.notifications ? {} : { notifications };
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
