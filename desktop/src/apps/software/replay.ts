// Replay status presentation, shared by Software's Replay view and the menu bar
// progress pill (PLAN.md §11).
import type { ReplayStatus } from "../../gen/aos/v1/types_pb";
import { ReplayState } from "../../gen/aos/v1/types_pb";

export function replayLabel(state: ReplayState): { text: string; mod: string } {
  switch (state) {
    case ReplayState.RUNNING:
      return { text: "Running", mod: "running" };
    case ReplayState.DONE:
      return { text: "Done", mod: "ok" };
    case ReplayState.FAILED:
      return { text: "Failed", mod: "bad" };
    default:
      return { text: "—", mod: "ok" };
  }
}

// replayActive is true only while a replay is still running, when the menu bar
// should show its progress.
export function replayActive(replay?: ReplayStatus): boolean {
  return !!replay && replay.state === ReplayState.RUNNING;
}
