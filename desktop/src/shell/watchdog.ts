// The frame watchdog (PLAN.md §4.3, §22, M4.6). Liquid Glass leans on the
// compositor's backdrop blur, which a weak GPU can't keep up with. The watchdog
// runs only while Glass is on, measures how often frames run over budget, and if
// they stay over budget for a sustained stretch it trips once — the Shell then
// switches Glass off and posts a notification.
//
// Two sources feed it, both reduced to "this frame was long":
//   • requestAnimationFrame gaps — the inter-frame time. Available everywhere,
//     and directly the smoothness the user feels.
//   • the Long Animation Frames API (long-animation-frame) where the browser
//     supports it — the same signal from the platform, so a long frame that
//     rAF alone might straddle still counts.
// Reports from the two within the same frame are de-duplicated by timestamp.

// A frame slower than this (ms) is "over budget": well past a 60 Hz frame
// (16.7 ms) and 30 Hz (33 ms), so ordinary jitter and the odd GC pause don't
// count — only frames a person would see stutter.
const FRAME_BUDGET_MS = 60;
// Bad frames are counted over this rolling window (ms)…
const WINDOW_MS = 3_000;
// …and the watchdog trips once this many fall inside it: a sustained stretch,
// not a single hiccup.
const TRIP_BAD_FRAMES = 6;

// startWatchdog begins measuring and calls onTrip at most once, when frames have
// been over budget for a sustained stretch. It returns a stop function; the
// caller stops it when Glass goes off (and the trip stops it too).
export function startWatchdog(onTrip: () => void): () => void {
  let stopped = false;
  let last = performance.now();
  let rafId = 0;
  const bad: number[] = [];

  function stop(): void {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(rafId);
    obs?.disconnect();
  }

  function mark(ts: number): void {
    if (stopped) return;
    // The same long frame can arrive from rAF and from the LoAF observer; count
    // it once.
    if (bad.length && ts - bad[bad.length - 1] < 8) return;
    bad.push(ts);
    const cutoff = ts - WINDOW_MS;
    while (bad.length && bad[0] < cutoff) bad.shift();
    if (bad.length >= TRIP_BAD_FRAMES) {
      stop();
      onTrip();
    }
  }

  function tick(): void {
    if (stopped) return;
    const now = performance.now();
    if (now - last > FRAME_BUDGET_MS) mark(now);
    last = now;
    rafId = requestAnimationFrame(tick);
  }
  rafId = requestAnimationFrame(tick);

  let obs: PerformanceObserver | undefined;
  try {
    const supported =
      typeof PerformanceObserver !== "undefined" &&
      PerformanceObserver.supportedEntryTypes?.includes("long-animation-frame");
    if (supported) {
      obs = new PerformanceObserver((list) => {
        for (const e of list.getEntries()) {
          if (e.duration > FRAME_BUDGET_MS) mark(performance.now());
        }
      });
      // buffered:false — only frames from now on, while Glass is actually up.
      obs.observe({ type: "long-animation-frame", buffered: false });
    }
  } catch {
    // The API is optional; the rAF meter covers browsers without it.
  }

  return stop;
}
