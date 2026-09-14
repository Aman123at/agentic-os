// The Approval pop-up (PLAN.md §7, §4.3, M3.4): when the Agent needs consent for
// a risky or Protected-Path action, aosd raises an Approval and this modal asks
// the user to Allow once, Allow for the rest of the Task, or Deny. Approvals
// arrive over the event stream into the store; the oldest pending one is shown.
import { useMemo, useState } from "react";

import type { Approval } from "../gen/aos/v1/types_pb";
import { ApprovalDecision } from "../gen/aos/v1/types_pb";
import { useDesktop } from "../store";

export default function Approvals() {
  const approvals = useDesktop((s) => s.approvals);
  const decideApproval = useDesktop((s) => s.decideApproval);
  const openTaskView = useDesktop((s) => s.openTaskView);
  const [busy, setBusy] = useState(false);

  // Show the oldest pending Approval first, so a queue clears in order.
  const pending = useMemo(
    () =>
      Object.values(approvals).sort(
        (a, b) => Number(a.createdAt?.seconds ?? 0n) - Number(b.createdAt?.seconds ?? 0n),
      ),
    [approvals],
  );
  const current: Approval | undefined = pending[0];
  if (!current) return null;

  async function decide(decision: ApprovalDecision) {
    setBusy(true);
    try {
      await decideApproval(current!.id, decision);
    } finally {
      setBusy(false);
    }
  }

  const protectedPaths = current.protectedPaths ?? [];

  return (
    <div className="scrim">
      <div className="approval" role="dialog" aria-modal="true" aria-label="Approval needed">
        <div className="approval__head">
          <span className="approval__badge">Approval needed</span>
          {pending.length > 1 && <span className="approval__more">+{pending.length - 1} more</span>}
        </div>
        <p className="approval__summary">{current.summary}</p>
        <p className="approval__tool">
          <code>{current.tool}</code>
          <button className="approval__link" onClick={() => openTaskView(current.taskId)}>
            View Task
          </button>
        </p>

        {current.reasons.length > 0 && (
          <ul className="approval__reasons">
            {current.reasons.map((r, i) => (
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
          {current.grantable && (
            <button className="approval__btn" disabled={busy} onClick={() => void decide(ApprovalDecision.ALLOW_FOR_TASK)}>
              Allow for this Task
            </button>
          )}
          <button className="approval__btn approval__btn--go" disabled={busy} onClick={() => void decide(ApprovalDecision.ALLOW_ONCE)}>
            Allow once
          </button>
        </div>
      </div>
    </div>
  );
}
