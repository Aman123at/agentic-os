// Formatters for Activity Monitor (PLAN.md §4.3, M4.4): sizes, rates and the
// Service state labels, kept apart from the components.
import type { ServiceState } from "../../gen/aos/v1/types_pb";
import { ServiceState as State } from "../../gen/aos/v1/types_pb";

// bytes humanises a byte count (1024-based), like the Finder's file sizes.
export function bytes(n: number): string {
  if (n < 1024) return `${Math.round(n)} B`;
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// rate shows a per-second byte rate, e.g. "1.2 MB/s".
export function rate(bytesPerSec: number): string {
  return `${bytes(bytesPerSec)}/s`;
}

export function percent(n: number): string {
  return `${n.toFixed(n < 10 ? 1 : 0)}%`;
}

// serviceState gives a Service's state a word and a CSS modifier.
export function serviceState(state: ServiceState): { text: string; mod: string } {
  switch (state) {
    case State.RUNNING:
      return { text: "Running", mod: "running" };
    case State.STOPPED:
      return { text: "Stopped", mod: "stopped" };
    case State.RESTARTING:
      return { text: "Restarting", mod: "restarting" };
    case State.FAILED:
      return { text: "Failed", mod: "failed" };
    default:
      return { text: "—", mod: "stopped" };
  }
}
