// A window's app may answer its close itself (PLAN.md M8.3): Code asks Save All
// / Don't Save / Cancel in its own dialog rather than the browser's confirm().
// A guard that returns false keeps the window open; the app then closes it with
// closeWindow(id, true) once the user has answered.
const guards = new Map<string, () => boolean>();

export function setCloseGuard(winId: string, guard: (() => boolean) | null): void {
  if (guard) guards.set(winId, guard);
  else guards.delete(winId);
}

export function mayClose(winId: string): boolean {
  return guards.get(winId)?.() ?? true;
}

// What a window's app ends when the window closes, not merely unmounts (a
// reload, or StrictMode's remount in development): Code's Terminal panel ends
// its Sessions with the window (M8.6).
const endings = new Map<string, Set<() => void>>();

export function onWindowClosed(winId: string, end: () => void): () => void {
  let set = endings.get(winId);
  if (!set) endings.set(winId, (set = new Set()));
  set.add(end);
  return () => void set.delete(end);
}

export function windowClosed(winId: string): void {
  const set = endings.get(winId);
  endings.delete(winId);
  set?.forEach((end) => end());
}
