// Small formatters for the Agent Task surface (PLAN.md §4.3, M3.4): Task state
// labels, token/cost lines and per-step presentation. Kept apart from the
// component so the Tasks chunk stays readable.
import type { Task, TaskStep, Usage } from "../../gen/aos/v1/types_pb";
import { StepKind, TaskState, ToolCallStatus } from "../../gen/aos/v1/types_pb";

// stateLabel gives a Task's state a word and a CSS modifier (running, ok, bad…).
export function stateLabel(state: TaskState): { text: string; mod: string } {
  switch (state) {
    case TaskState.QUEUED:
      return { text: "Queued", mod: "queued" };
    case TaskState.RUNNING:
      return { text: "Running", mod: "running" };
    case TaskState.AWAITING_USER:
      return { text: "Waiting for you", mod: "await" };
    case TaskState.SUCCEEDED:
      return { text: "Done", mod: "ok" };
    case TaskState.FAILED:
      return { text: "Failed", mod: "bad" };
    case TaskState.CANCELLED:
      return { text: "Cancelled", mod: "bad" };
    case TaskState.INTERRUPTED:
      return { text: "Interrupted", mod: "bad" };
    default:
      return { text: "—", mod: "queued" };
  }
}

export function isLive(state: TaskState): boolean {
  return state === TaskState.QUEUED || state === TaskState.RUNNING || state === TaskState.AWAITING_USER;
}

export function isFinished(state: TaskState): boolean {
  return state === TaskState.SUCCEEDED || state === TaskState.FAILED || state === TaskState.CANCELLED;
}

// costLine renders tokens and estimated spend (PLAN.md §8.4). cost_known is
// false when prices.yaml has no price for a model that ran.
export function costLine(usage?: Usage): string {
  if (!usage) return "";
  const inTok = Number(usage.inputTokens);
  const cached = Number(usage.cachedInputTokens);
  const out = Number(usage.outputTokens);
  const tokens = `${fmtNum(inTok)} in${cached ? ` (${fmtNum(cached)} cached)` : ""} · ${fmtNum(out)} out`;
  const cost = usage.costKnown ? `$${usage.costUsd.toFixed(4)}` : "cost unknown";
  return `${tokens} · ${cost}`;
}

function fmtNum(n: number): string {
  return n.toLocaleString();
}

export function stepIcon(step: TaskStep): string {
  switch (step.kind) {
    case StepKind.USER_MESSAGE:
      return "🧑";
    case StepKind.AGENT_TEXT:
      return "🤖";
    case StepKind.TOOL_CALL:
      return toolStatusIcon(step.toolCall?.status);
    case StepKind.NOTE:
      return "ℹ️";
    default:
      return "•";
  }
}

function toolStatusIcon(status?: ToolCallStatus): string {
  switch (status) {
    case ToolCallStatus.RUNNING:
    case ToolCallStatus.PENDING:
      return "⚙️";
    case ToolCallStatus.AWAITING_APPROVAL:
      return "🔐";
    case ToolCallStatus.SUCCEEDED:
      return "✅";
    case ToolCallStatus.FAILED:
      return "❌";
    case ToolCallStatus.DENIED:
      return "⛔";
    case ToolCallStatus.CANCELLED:
      return "🚫";
    default:
      return "🔧";
  }
}

// toolSummary is a one-line description of a tool call: the tool name and a
// compact peek at its arguments.
export function toolSummary(step: TaskStep): string {
  const tc = step.toolCall;
  if (!tc) return "";
  let args = tc.argumentsJson || "";
  if (args.length > 140) args = `${args.slice(0, 140)}…`;
  return `${tc.tool} ${args}`.trim();
}

export function taskTitle(task?: Task): string {
  if (!task) return "Agent Task";
  return task.title || task.prompt.slice(0, 60) || "Agent Task";
}
