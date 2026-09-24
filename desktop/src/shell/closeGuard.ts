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
