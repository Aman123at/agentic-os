// Software's Install Ledger view (PLAN.md §11): every root operation — an
// install, removal, command, Service change or Restore — newest first, from
// SoftwareService.ListLedger, paged as you ask for more. Each operation shows
// the before and after state of everything it changed (a package version, an
// /etc file, a Service).
import { useCallback, useEffect, useState } from "react";

import { software } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { LedgerOp } from "../../gen/aos/v1/types_pb";
import { useDesktop } from "../../store";
import { taskTitle, when } from "../agent/format";

const PAGE = 50;

const ACTION_ICON: Record<string, string> = {
  install: "⬇️",
  remove: "🗑️",
  command: "⌨️",
  service: "🛰️",
  restore: "⏪",
};

export default function Ledger() {
  const [ops, setOps] = useState<LedgerOp[]>([]);
  const [end, setEnd] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const tasks = useDesktop((s) => s.tasks);

  const load = useCallback(async (fresh: boolean, beforeId: bigint) => {
    setLoading(true);
    try {
      const resp = await software.listLedger({ limit: PAGE, beforeId });
      setError("");
      setOps((old) => (fresh ? resp.ops : [...old, ...resp.ops]));
      setEnd(resp.ops.length < PAGE);
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(true, 0n);
  }, [load]);

  return (
    <div className="ledger">
      <div className="sw__toolbar">
        <span className="activity__count">{loading && ops.length === 0 ? "Loading…" : `${ops.length}${end ? "" : "+"} operation${ops.length === 1 ? "" : "s"}`}</span>
        <button className="tasks__btn" onClick={() => void load(true, 0n)}>
          Refresh
        </button>
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      {!loading && ops.length === 0 && !error ? (
        <div className="agent__empty">The Install Ledger is empty.</div>
      ) : (
        <div className="ledger__list">
          {ops.map((op) => (
            <article key={op.id.toString()} className="ledger__op">
              <header className="ledger__ophead">
                <span className="ledger__icon" aria-hidden="true">
                  {ACTION_ICON[op.action] ?? "•"}
                </span>
                <span className="ledger__summary">{op.summary || op.action}</span>
                <span className="ledger__time">{when(op.time, true)}</span>
              </header>
              <div className="ledger__meta">
                <span className={`ledger__action ledger__action--${op.action}`}>{op.action}</span>
                {op.actor && <span> · {op.actor}</span>}
                {op.taskId && <span title={op.taskId}> · {tasks[op.taskId] ? taskTitle(tasks[op.taskId]) : op.taskId}</span>}
              </div>
              {op.changes.length > 0 && (
                <ul className="ledger__changes">
                  {op.changes.map((c, i) => (
                    <li key={i} className="ledger__change">
                      <span className="ledger__ckind">{c.manager || c.kind}</span>
                      <span className="ledger__cname">{c.name}</span>
                      <span className="ledger__cstate">
                        <span className="ledger__before">{c.before || "absent"}</span>
                        <span className="ledger__arrow" aria-hidden="true">→</span>
                        <span className="ledger__after">{c.after || "absent"}</span>
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </article>
          ))}
          {!end && (
            <button className="tasks__btn ledger__more" disabled={loading} onClick={() => void load(false, ops.at(-1)?.id ?? 0n)}>
              {loading ? "Loading…" : "Load more"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
