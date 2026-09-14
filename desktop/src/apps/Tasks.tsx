// The Agent Task surface (PLAN.md §4.3, M3.4): a live step feed for one Task,
// with its tokens and cost, plus the controls to answer a question, send a
// Follow-up, cancel or resume. It is deliberately minimal — the full Agent app,
// audit browser and usage charts are M4. State flows in through the store's
// event stream (§4.3 rule 4); this component only reads a slice and renders.
import { useEffect, useRef, useState } from "react";

import type { TaskStep } from "../gen/aos/v1/types_pb";
import { AwaitingKind, StepKind, TaskState } from "../gen/aos/v1/types_pb";
import { useDesktop } from "../store";
import { costLine, isFinished, stateLabel, stepIcon, taskTitle, toolSummary } from "./tasks/format";

export default function Tasks() {
  const openTask = useDesktop((s) => s.openTask);
  const task = useDesktop((s) => (s.openTask ? s.tasks[s.openTask] : undefined));
  const steps = useDesktop((s) => s.steps);
  const { loadTask, answerQuestion, sendFollowUp, cancelTask, resumeTask } = useDesktop();
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);
  const feed = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (openTask) void loadTask(openTask);
  }, [openTask, loadTask]);

  // Keep the newest step in view as the feed streams.
  useEffect(() => {
    const el = feed.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [steps]);

  if (!openTask || !task) {
    return (
      <div className="tasks tasks--empty">
        <p className="tasks__emoji">🤖</p>
        <p>No Task open.</p>
        <p className="tasks__hint">Start one from Spotlight (⌥Space, or ^Space on Windows).</p>
      </div>
    );
  }

  const st = stateLabel(task.state);
  const awaiting = task.awaiting;
  const question = awaiting && awaiting.kind !== AwaitingKind.APPROVAL ? awaiting.question : "";
  const waitingApproval = awaiting?.kind === AwaitingKind.APPROVAL;
  const running = task.state === TaskState.RUNNING || task.state === TaskState.QUEUED;
  const finished = isFinished(task.state);
  const interrupted = task.state === TaskState.INTERRUPTED;
  const canReply = Boolean(question) || finished || interrupted;
  const replyLabel = question ? "Answer" : "Follow-up";

  async function submit() {
    const text = reply.trim();
    if (!text || busy) return;
    setBusy(true);
    try {
      if (question) await answerQuestion(openTask, text);
      else await sendFollowUp(openTask, text);
      setReply("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="tasks">
      <header className="tasks__head">
        <div className="tasks__titlerow">
          <span className="tasks__title" title={task.prompt}>{taskTitle(task)}</span>
          <span className={`tasks__state tasks__state--${st.mod}`}>{st.text}</span>
        </div>
        <div className="tasks__meta">
          <span title="Model">{task.model || "—"}</span>
          <span title="Tokens and estimated cost">{costLine(task.usage)}</span>
        </div>
      </header>

      <div className="tasks__feed" ref={feed}>
        {steps.map((s) => (
          <Step key={s.id} step={s} />
        ))}
        {steps.length === 0 && <div className="tasks__loading">Loading steps…</div>}
      </div>

      {finished && task.summary && (
        <div className={`tasks__summary${task.state === TaskState.FAILED ? " tasks__summary--bad" : ""}`}>
          {task.summary}
        </div>
      )}

      {waitingApproval && <div className="tasks__await">Waiting for your approval — see the pop-up.</div>}

      {question && (
        <div className="tasks__question">
          <span className="tasks__qicon">
            {awaiting?.kind === AwaitingKind.QUESTION ? "❓" : awaiting?.kind === AwaitingKind.COST_LIMIT ? "💸" : "🔁"}
          </span>
          <span>{question}</span>
        </div>
      )}

      <footer className="tasks__foot">
        {canReply && (
          <textarea
            className="tasks__reply"
            placeholder={question ? "Type your answer…" : "Ask a follow-up…"}
            value={reply}
            onChange={(e) => setReply(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                void submit();
              }
            }}
          />
        )}
        <div className="tasks__actions">
          {running && (
            <button className="tasks__btn tasks__btn--danger" onClick={() => void cancelTask(openTask)}>
              Cancel
            </button>
          )}
          {interrupted && (
            <button className="tasks__btn" onClick={() => void resumeTask(openTask)}>
              Resume
            </button>
          )}
          {canReply && (
            <button className="tasks__btn tasks__btn--go" onClick={() => void submit()} disabled={busy || !reply.trim()}>
              {replyLabel}
            </button>
          )}
        </div>
      </footer>
    </div>
  );
}

function Step({ step }: { step: TaskStep }) {
  const tool = step.kind === StepKind.TOOL_CALL;
  const text = tool ? toolSummary(step) : step.text;
  const result = tool ? step.toolCall?.result : "";
  return (
    <div className={`tasks__step tasks__step--${kindClass(step.kind)}`}>
      <span className="tasks__stepicon">{stepIcon(step)}</span>
      <div className="tasks__stepbody">
        <div className="tasks__steptext">{text}</div>
        {tool && result && <div className="tasks__stepresult">{result}</div>}
      </div>
    </div>
  );
}

function kindClass(kind: StepKind): string {
  switch (kind) {
    case StepKind.USER_MESSAGE:
      return "user";
    case StepKind.AGENT_TEXT:
      return "agent";
    case StepKind.TOOL_CALL:
      return "tool";
    default:
      return "note";
  }
}
