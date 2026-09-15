// Live folder listings over FileService.Watch (PLAN.md §15). Every Finder window
// and the Downloads stack subscribe here rather than calling Watch themselves:
// two views of one folder share a stream, and only a few streams stay open.
// Browsers allow six HTTP/1.1 connections to aosd, the event stream holds one,
// and a stream per window would leave RPCs queued behind them. A folder beyond
// the stream cap is listed every 2 s instead, the same cadence Watch polls at.
import { ConnectError } from "@connectrpc/connect";

import type { FileInfo } from "../gen/aos/v1/services_pb";
import { files } from "./client";

const MAX_STREAMS = 3;
const INTERVAL = 2000;
const RETRY = 5000;

export interface FolderListener {
  onEntries: (entries: FileInfo[]) => void;
  onError: (message: string) => void;
}

interface Watched {
  listeners: Set<FolderListener>;
  entries?: FileInfo[];
  key: string;
  stream: boolean;
  stop: AbortController;
}

const watched = new Map<string, Watched>();

// watchFolder calls onEntries with the folder's listing now and each time it
// changes, until the returned function is called.
export function watchFolder(path: string, listener: FolderListener): () => void {
  let w = watched.get(path);
  if (!w) {
    const streams = [...watched.values()].filter((x) => x.stream).length;
    w = { listeners: new Set(), key: "", stream: streams < MAX_STREAMS, stop: new AbortController() };
    watched.set(path, w);
    void (w.stream ? runStream : runPoll)(path, w);
  } else if (w.entries) {
    listener.onEntries(w.entries);
  }
  w.listeners.add(listener);
  const mine = w;
  return () => {
    mine.listeners.delete(listener);
    if (mine.listeners.size === 0) {
      mine.stop.abort();
      if (watched.get(path) === mine) watched.delete(path);
    }
  };
}

function publish(w: Watched, entries: FileInfo[]) {
  const key = listingKey(entries);
  if (w.entries && key === w.key) return;
  w.entries = entries;
  w.key = key;
  for (const l of [...w.listeners]) l.onEntries(entries);
}

function fail(w: Watched, err: unknown) {
  const message = ConnectError.from(err).message;
  for (const l of [...w.listeners]) l.onError(message);
}

async function runStream(path: string, w: Watched) {
  const signal = w.stop.signal;
  while (!signal.aborted) {
    try {
      for await (const resp of files.watch({ path }, { signal })) publish(w, resp.entries);
    } catch (err) {
      if (signal.aborted) return;
      fail(w, err);
    }
    await sleep(RETRY, signal);
  }
}

async function runPoll(path: string, w: Watched) {
  const signal = w.stop.signal;
  while (!signal.aborted) {
    try {
      const resp = await files.list({ path }, { signal });
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
  return entries.map((e) => `${e.path}\t${e.size}\t${e.modifiedAt?.seconds ?? ""}\t${e.dir}\t${e.protected}`).join("\n");
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
