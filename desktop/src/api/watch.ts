// Live folder listings over FileService.Watch (PLAN.md §15). Every Finder window
// and the Downloads stack subscribe here rather than calling Watch themselves,
// because long-lived requests are scarce: browsers allow six HTTP/1.1
// connections to aosd across all the Desktop's tabs, and each tab's event
// stream already holds one. Held streams beyond that leave RPCs, and even a
// reload, queued behind them.
//
// So a tab keeps one Watch stream, for the folder most recently opened; views
// of the same folder share it. Other folders are listed every 2 s, the cadence
// Watch polls at on aosd anyway. A hidden tab watches nothing, and lists again
// as soon as it is shown.
import { ConnectError } from "@connectrpc/connect";

import type { FileInfo } from "../gen/aos/v1/services_pb";
import { files } from "./client";

const MAX_STREAMS = 1;
const INTERVAL = 2000;
const RETRY = 5000;

export interface FolderListener {
  onEntries: (entries: FileInfo[]) => void;
  onError: (message: string) => void;
}

type Mode = "stream" | "poll" | "idle";

interface Watched {
  path: string;
  listeners: Set<FolderListener>;
  entries?: FileInfo[];
  key: string;
  opened: number; // when a view last subscribed; the newest gets the stream
  mode: Mode;
  stop?: AbortController;
}

const watched = new Map<string, Watched>();
let clock = 0;

// watchFolder calls onEntries with the folder's listing now and each time it
// changes, until the returned function is called.
export function watchFolder(path: string, listener: FolderListener): () => void {
  let w = watched.get(path);
  if (!w) {
    w = { path, listeners: new Set(), key: "", opened: 0, mode: "idle" };
    watched.set(path, w);
  } else if (w.entries) {
    listener.onEntries(w.entries);
  }
  w.listeners.add(listener);
  w.opened = ++clock;
  rebalance();
  const mine = w;
  return () => {
    mine.listeners.delete(listener);
    if (mine.listeners.size > 0) return;
    mine.stop?.abort();
    if (watched.get(path) === mine) watched.delete(path);
    rebalance();
  };
}

// rebalance gives each watched folder the way it should be watched now, and
// restarts only those whose way changed.
function rebalance() {
  const hidden = typeof document !== "undefined" && document.visibilityState === "hidden";
  const newest = [...watched.values()].sort((a, b) => b.opened - a.opened);
  newest.forEach((w, i) => {
    const mode: Mode = hidden ? "idle" : i < MAX_STREAMS ? "stream" : "poll";
    if (mode === w.mode) return;
    w.stop?.abort();
    w.mode = mode;
    if (mode === "idle") return;
    w.stop = new AbortController();
    void (mode === "stream" ? runStream : runPoll)(w, w.stop.signal);
  });
}

if (typeof document !== "undefined") document.addEventListener("visibilitychange", rebalance);

function publish(w: Watched, entries: FileInfo[]) {
  const key = listingKey(entries);
  if (w.entries && key === w.key) return;
  w.entries = entries;
  w.key = key;
  for (const l of [...w.listeners]) l.onEntries(entries);
}

function fail(w: Watched, err: unknown) {
  const message = ConnectError.from(err).message;
  // The next listing is sent even if unchanged, so views clear the error.
  w.entries = undefined;
  for (const l of [...w.listeners]) l.onError(message);
}

async function runStream(w: Watched, signal: AbortSignal) {
  while (!signal.aborted) {
    try {
      for await (const resp of files.watch({ path: w.path }, { signal })) publish(w, resp.entries);
    } catch (err) {
      if (signal.aborted) return;
      fail(w, err);
    }
    await sleep(RETRY, signal);
  }
}

async function runPoll(w: Watched, signal: AbortSignal) {
  while (!signal.aborted) {
    try {
      const resp = await files.list({ path: w.path }, { signal });
      if (!signal.aborted) publish(w, resp.entries);
    } catch (err) {
      if (signal.aborted) return;
      fail(w, err);
    }
    await sleep(INTERVAL, signal);
  }
}

// listingKey is what a listing's rows show, so an unchanged folder does not
// render again.
export function listingKey(entries: FileInfo[]): string {
  return entries.map((e) => `${e.path}\t${e.size}\t${e.modifiedAt?.seconds ?? ""}.${e.modifiedAt?.nanos ?? ""}\t${e.dir}\t${e.protected}`).join("\n");
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}
