// Frame metrics from a Chrome trace (PLAN.md §16, M3.5). Headless Chromium runs
// its own frame clock, so rAF spacing says nothing about how much work a frame
// costs. The trace does:
//   • ProxyMain::BeginMainFrame spans one frame's main-thread work — input
//     dispatch, rAF callbacks, style, layout, paint and commit. Its duration is
//     the frame's cost; under 16.7 ms means the frame fits a 60 Hz budget.
//   • PipelineReporter tags each compositor frame with how it was presented, so
//     a frame the main thread missed shows up as dropped or partially presented.

// The trace categories frameStats needs.
export const FRAME_CATEGORIES = [
  "cc",
  "benchmark",
  "devtools.timeline",
  "disabled-by-default-devtools.timeline.frame",
  "blink.user_timing",
];

interface TraceEvent {
  name: string;
  ph: string;
  pid: number;
  tid: number;
  ts: number; // µs
  dur?: number; // µs
  args?: {
    name?: string;
    frame_reporter?: { state?: string };
  };
}

export interface FrameStats {
  frames: number; // main-thread frames between the marks
  p95: number; // ms of main-thread work per frame
  max: number; // ms
  missed: number; // frames dropped or presented without the main thread's update
}

// frameStats measures the page's renderer frames between two performance.mark()
// names recorded in the trace.
export function frameStats(trace: Buffer, startMark: string, endMark: string): FrameStats {
  const events = (JSON.parse(trace.toString("utf8")) as { traceEvents: TraceEvent[] }).traceEvents;

  const markTs = (name: string) => {
    const m = events.find((e) => e.name === name);
    if (!m) throw new Error(`mark ${name} not in the trace (is blink.user_timing traced?)`);
    return m.ts;
  };
  const from = markTs(startMark);
  const to = markTs(endMark);

  // The page's renderer main thread is the one running its frames.
  const mains = new Set(
    events
      .filter((e) => e.ph === "M" && e.name === "thread_name" && e.args?.name === "CrRendererMain")
      .map((e) => `${e.pid}/${e.tid}`),
  );
  const perThread = new Map<string, TraceEvent[]>();
  for (const e of events) {
    if (e.name !== "ProxyMain::BeginMainFrame" || e.ph !== "X") continue;
    const key = `${e.pid}/${e.tid}`;
    if (!mains.has(key) || e.ts < from || e.ts > to) continue;
    perThread.set(key, [...(perThread.get(key) ?? []), e]);
  }
  const [main] = [...perThread.entries()].sort((a, b) => b[1].length - a[1].length);
  if (!main) return { frames: 0, p95: 0, max: 0, missed: 0 };
  const pid = main[1][0].pid;

  const costs = main[1].map((e) => (e.dur ?? 0) / 1000).sort((a, b) => a - b);
  const missed = events.filter(
    (e) =>
      e.name === "PipelineReporter" &&
      e.ph === "b" &&
      e.pid === pid &&
      e.ts >= from &&
      e.ts <= to &&
      (e.args?.frame_reporter?.state === "STATE_DROPPED" ||
        e.args?.frame_reporter?.state === "STATE_PRESENTED_PARTIAL"),
  ).length;

  return {
    frames: costs.length,
    p95: costs[Math.min(costs.length - 1, Math.floor(costs.length * 0.95))],
    max: costs[costs.length - 1],
    missed,
  };
}
