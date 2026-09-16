// One Task in the Agent app: its live step feed with tokens and cost, its pending
// Approvals inline, and a composer for Follow-ups and answers, plus Cancel and
// Resume. State flows in through the store's event stream (§4.3 rule 4); this
// component only reads a slice and renders.
import { useEffect, useMemo, useRef, useState } from "react";

import type { TaskStep } from "../../gen/aos/v1/types_pb";
import { AwaitingKind, StepKind, TaskState } from "../../gen/aos/v1/types_pb";
import { ApprovalCard, sortApprovals } from "../../shell/Approvals";
import { useDesktop } from "../../store";
import { Confirm } from "../../ui/Confirm";
import { costLine, isFinished, isLive, stateLabel, stepIcon, taskTitle, toolSummary } from "./format";
import { friendlyError } from "../../api/error";

export default function TaskDetail() {
  const openTask = useDesktop((s) => s.openTask);
  const task = useDesktop((s) => (s.openTask ? s.tasks[s.openTask] : undefined));
  const steps = useDesktop((s) => s.steps);
  const allApprovals = useDesktop((s) => s.approvals);
  const { answerQuestion, sendFollowUp, cancelTask, resumeTask, deleteTask } = useDesktop();
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const feed = useRef<HTMLDivElement>(null);

  const approvals = useMemo(
    () => sortApprovals(Object.values(allApprovals).filter((a) => a.taskId === openTask)),
    [allApprovals, openTask],
  );

  // A different Task starts with an empty composer.
  useEffect(() => {
    setReply("");
    setError("");
  }, [openTask]);

  // Keep the newest step, or a new Approval, in view as the feed streams.
  useEffect(() => {
    const el = feed.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [steps, approvals.length]);

  if (!openTask || !task) {
    return (
      <div className="tasks tasks--empty">
        <p className="tasks__emoji">🤖</p>
        <p>{openTask ? "Loading the Task…" : "No Task selected."}</p>
        <p className="tasks__hint">Pick one from the list, or start one from Spotlight (⌥Space, or ^Space on Windows).</p>
      </div>
    );
  }

  const st = stateLabel(task.state);
  const awaiting = task.awaiting;
  const question = awaiting && awaiting.kind !== AwaitingKind.APPROVAL ? awaiting.question : "";
  const cancellable = task.state === TaskState.RUNNING || task.state === TaskState.QUEUED || task.state === TaskState.AWAITING_USER;
  const finished = isFinished(task.state);
  const interrupted = task.state === TaskState.INTERRUPTED;
  const canReply = Boolean(question) || finished || interrupted;

  async function act(f: () => Promise<void>) {
    setError("");
    try {
      await f();
    } catch (err) {
      setError(friendlyError(err));
    }
  }

  async function submit() {
    const text = reply.trim();
    if (!text || busy || !canReply) return;
    setBusy(true);
    await act(async () => {
      if (question) await answerQuestion(openTask, text);
      else await sendFollowUp(openTask, text);
      setReply("");
    });
    setBusy(false);
  }

  return (
    <div className="tasks">
      <header className="tasks__head">
        <div className="tasks__titlerow">
          <span className="tasks__title" title={task.prompt}>
            {taskTitle(task)}
          </span>
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
        {approvals.map((a) => (
          <div key={a.id} className="approval approval--inline" role="group" aria-label="Approval for this Task">
            <div className="approval__head">
              <span className="approval__badge">Approval needed</span>
            </div>
            <ApprovalCard approval={a} />
          </div>
        ))}
      </div>

      {finished && task.summary && (
        <div className={`tasks__summary${task.state === TaskState.FAILED ? " tasks__summary--bad" : ""}`}>{task.summary}</div>
      )}

      {question && (
        <div className="tasks__question">
          <span className="tasks__qicon">
            {awaiting?.kind === AwaitingKind.QUESTION ? "❓" : awaiting?.kind === AwaitingKind.COST_LIMIT ? "💸" : "🔁"}
          </span>
          <span>{question}</span>
        </div>
      )}

      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <footer className="tasks__foot">
        <textarea
          className="tasks__reply"
          aria-label="Message the Agent"
          placeholder={question ? "Type your answer…" : canReply ? "Ask a follow-up… (⌘Enter to send)" : "The Agent is working. You can reply when it asks or finishes."}
          value={reply}
          disabled={!canReply}
          onChange={(e) => setReply(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              void submit();
            }
          }}
        />
        <div className="tasks__actions">
          {cancellable && (
            <button className="tasks__btn tasks__btn--danger" onClick={() => void act(() => cancelTask(openTask))}>
              Cancel
            </button>
          )}
          {interrupted && (
            <button className="tasks__btn" onClick={() => void act(() => resumeTask(openTask))}>
              Resume
            </button>
          )}
          {!isLive(task.state) && (
            <button className="tasks__btn tasks__btn--danger" onClick={() => setConfirming(true)}>
              Delete
            </button>
          )}
          <button className="tasks__btn tasks__btn--go" onClick={() => void submit()} disabled={busy || !canReply || !reply.trim()}>
            {question ? "Answer" : "Send"}
          </button>
        </div>
      </footer>
      {confirming && (
        <Confirm
          title="Delete Task?"
          message={`Delete “${taskTitle(task)}” and its steps, Approvals and grants. The Audit Log keeps its record. This cannot be undone.`}
          confirmLabel="Delete"
          danger
          onConfirm={() => {
            setConfirming(false);
            void act(() => deleteTask(openTask));
          }}
          onCancel={() => setConfirming(false)}
        />
      )}
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
