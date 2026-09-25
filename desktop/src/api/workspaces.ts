// Code's Workspaces on the tab's event stream (PLAN.md M8.2, M8.3). A Code
// window holds no stream of its own: browsers allow six connections to aosd
// across all the Desktop's tabs. Instead its Workspace belongs to the tab's
// event subscription, and the folder's changes arrive on that stream as
// WorkspaceChanged events, which the store hands to workspaceEvent.
//
// A Workspace lasts as long as the subscription: when the stream drops and
// reconnects, aosd has already closed it, so every open Workspace is opened
// again on the new subscription and its window told to look again (onOpened).
import { Code, ConnectError } from "@connectrpc/connect";

import type { OpenWorkspaceResponse, WorkspaceChanged } from "../gen/aos/v1/services_pb";
import { code } from "./client";

export interface WorkspaceListener {
  // The Workspace is open: the first time, and again after each reconnect,
  // when changes may have been missed in between.
  onOpened: (ws: OpenWorkspaceResponse, again: boolean) => void;
  onChanged: (ev: WorkspaceChanged) => void;
  onError: (err: ConnectError) => void;
}

export interface WorkspaceHandle {
  // Tells aosd which of the window's files are unsaved, for the Realm switch's
  // warning (M8.13). Sent again after a reconnect.
  reportDirty: (paths: string[]) => void;
  close: () => void;
}

interface Open {
  path: string;
  win: string; // the Code window's id
  root: string; // aosd's name for the folder, once opened
  listener: WorkspaceListener;
  id: string; // "" until opened on the current subscription
  opened: boolean; // opened at least once
  closed: boolean;
  dirty: string[];
  report: Promise<unknown>; // the ReportDirty calls, in order
}

// Events can arrive before the OpenWorkspace reply that names their Workspace;
// they wait here, a bounded number, until it does.
const EARLY_MAX = 256;

let subscription = "";
const all = new Set<Open>();
const byId = new Map<string, Open>();
let early: WorkspaceChanged[] = [];

// setSubscription is called with the event stream's subscription id when it
// connects and "" when it drops.
export function setSubscription(id: string): void {
  if (id === subscription) return;
  subscription = id;
  byId.clear();
  early = [];
  for (const o of all) {
    o.id = "";
    if (id) void attach(o);
  }
}

// workspaceEvent routes one WorkspaceChanged event to its window.
export function workspaceEvent(ev: WorkspaceChanged): void {
  const o = byId.get(ev.workspaceId);
  if (o) {
    o.listener.onChanged(ev);
    return;
  }
  early.push(ev);
  if (early.length > EARLY_MAX) early = early.slice(-EARLY_MAX);
}

// openWorkspace opens the folder at path for the Code window win (as the
// Realm's user, on aosd) and keeps it open, across reconnects, until the handle
// is closed.
export function openWorkspace(path: string, win: string, listener: WorkspaceListener): WorkspaceHandle {
  const o: Open = { path, win, root: "", listener, id: "", opened: false, closed: false, dirty: [], report: Promise.resolve() };
  all.add(o);
  if (subscription) void attach(o);
  return {
    reportDirty: (paths) => {
      o.dirty = paths;
      if (o.id) send(o);
    },
    close: () => {
      if (o.closed) return;
      o.closed = true;
      all.delete(o);
      if (o.id) {
        byId.delete(o.id);
        void code.closeWorkspace({ workspaceId: o.id }).catch(() => {});
      }
    },
  };
}

async function attach(o: Open): Promise<void> {
  const sub = subscription;
  let ws: OpenWorkspaceResponse;
  try {
    ws = await code.openWorkspace({ subscriptionId: sub, path: o.path });
  } catch (err) {
    // A subscription that ended meanwhile brings its own attach when the stream
    // reconnects; anything else is the window's to show.
    if (o.closed || sub !== subscription) return;
    const e = ConnectError.from(err);
    if (e.code === Code.NotFound && e.message.includes("subscription")) return;
    o.listener.onError(e);
    return;
  }
  if (o.closed) {
    void code.closeWorkspace({ workspaceId: ws.id }).catch(() => {});
    return;
  }
  if (sub !== subscription) return; // aosd closed it with its subscription
  o.id = ws.id;
  o.root = ws.root;
  byId.set(ws.id, o);
  const again = o.opened;
  o.opened = true;
  o.listener.onOpened(ws, again);
  const mine = early.filter((ev) => ev.workspaceId === ws.id);
  if (mine.length) {
    early = early.filter((ev) => ev.workspaceId !== ws.id);
    for (const ev of mine) o.listener.onChanged(ev);
  }
  if (o.dirty.length) send(o);
}

function send(o: Open): void {
  const id = o.id;
  o.report = o.report.then(() => (o.closed || o.id !== id ? undefined : code.reportDirty({ workspaceId: id, paths: o.dirty }).catch(() => {})));
}

// OpenCode is one of this tab's Code windows and its Workspace.
export interface OpenCode {
  id: string; // aosd's id for the Workspace; "" until it is open
  path: string;
  win: string;
  unsaved: string[];
}

// openCode lists this tab's Code windows, for the Realm switch (M8.13): it is
// refused while any is open, and Restart AOS names their unsaved files. It is
// asked here first, at once; aosd's answer adds Code open in other tabs.
export function openCode(): OpenCode[] {
  return [...all].map((o) => ({ id: o.id, path: o.root || o.path, win: o.win, unsaved: [...o.dirty] }));
}
