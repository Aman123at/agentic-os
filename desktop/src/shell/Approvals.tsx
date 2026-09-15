// The Approval pop-up (PLAN.md §7, §4.3, M3.4): when the Agent needs consent for
// a risky or Protected-Path action, aosd raises an Approval and this modal asks
// the user to Allow once, Allow for the rest of the Task, or Deny. Approvals
// arrive over the event stream into the store; the oldest pending one is shown.
// While the Agent app has the focus and shows a Task, that Task's Approvals are
// answered inline there instead (ApprovalCard), so they do not pop up.
import { useMemo, useState, type ReactNode } from "react";

import type { Approval } from "../gen/aos/v1/types_pb";
import { ApprovalDecision } from "../gen/aos/v1/types_pb";
import { useDesktop } from "../store";

export default function Approvals() {
  const approvals = useDesktop((s) => s.approvals);
  const openTaskView = useDesktop((s) => s.openTaskView);
  const inlineTask = useDesktop(inlineApprovalTask);

  // Show the oldest pending Approval first, so a queue clears in order.
  const pending = useMemo(() => sortApprovals(Object.values(approvals).filter((a) => a.taskId !== inlineTask)), [approvals, inlineTask]);
  const current: Approval | undefined = pending[0];
  if (!current) return null;

  return (
    <div className="scrim">
      <div className="approval" role="dialog" aria-modal="true" aria-label="Approval needed">
        <div className="approval__head">
          <span className="approval__badge">Approval needed</span>
          {pending.length > 1 && <span className="approval__more">+{pending.length - 1} more</span>}
        </div>
        <ApprovalCard
          key={current.id}
          approval={current}
          toolExtra={
            <button className="approval__link" onClick={() => openTaskView(current.taskId)}>
              View Task
            </button>
          }
        />
      </div>
    </div>
  );
}

// inlineApprovalTask is the Task whose Approvals the Agent app shows inline: the
// one it shows while its window has the focus on the Tasks view, or "".
function inlineApprovalTask(s: ReturnType<typeof useDesktop.getState>): string {
  const win = s.windows.find((w) => w.appId === "agent");
  if (!win || win.minimized || s.focused !== win.id || (win.state?.view ?? "tasks") !== "tasks") return "";
  return s.openTask;
}

export function sortApprovals(list: Approval[]): Approval[] {
  return list.sort((a, b) => Number(a.createdAt?.seconds ?? 0n) - Number(b.createdAt?.seconds ?? 0n));
}

// ApprovalCard is one Approval's summary, reasons, 🔒 Protected Paths and
// decision buttons, in the pop-up or inline in the Agent app.
export function ApprovalCard({ approval, toolExtra }: { approval: Approval; toolExtra?: ReactNode }) {
  const decideApproval = useDesktop((s) => s.decideApproval);
  const [busy, setBusy] = useState(false);

  async function decide(decision: ApprovalDecision) {
    setBusy(true);
    try {
      await decideApproval(approval.id, decision);
    } finally {
      setBusy(false);
    }
  }

  const protectedPaths = approval.protectedPaths ?? [];

  return (
    <>
      <p className="approval__summary">{approval.summary}</p>
      <p className="approval__tool">
        <code>{approval.tool}</code>
        {toolExtra}
      </p>

      {approval.reasons.length > 0 && (
        <ul className="approval__reasons">
          {approval.reasons.map((r, i) => (
            <li key={i}>{r}</li>
          ))}
        </ul>
      )}

      {protectedPaths.length > 0 && (
        <div className="approval__protected">
          <strong>🔒 Protected Paths this would change:</strong>
          <ul>
            {protectedPaths.map((p) => (
              <li key={p}>
                <code>{p}</code>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="approval__actions">
        <button className="approval__btn approval__btn--deny" disabled={busy} onClick={() => void decide(ApprovalDecision.DENY)}>
          Deny
        </button>
        {approval.grantable && (
          <button className="approval__btn" disabled={busy} onClick={() => void decide(ApprovalDecision.ALLOW_FOR_TASK)}>
            Allow for this Task
          </button>
        )}
        <button className="approval__btn approval__btn--go" disabled={busy} onClick={() => void decide(ApprovalDecision.ALLOW_ONCE)}>
          Allow once
        </button>
      </div>
    </>
  );
}
