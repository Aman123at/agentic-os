// Software's Checkpoints view (PLAN.md §11): the points you can return the
// Machine's software, /etc and Services to. You can take one, or Restore to one
// after a confirmation; a Restore first takes a "Before Restore" Checkpoint so it
// too can be undone, and anything that could not be restored exactly is shown.
import { useCallback, useEffect, useState } from "react";

import { software } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { Checkpoint } from "../../gen/aos/v1/types_pb";
import { useDesktop } from "../../store";
import { Confirm } from "../../ui/Confirm";
import { taskTitle, when } from "../agent/format";

interface Result {
  name: string;
  notes: string[];
  before?: Checkpoint;
}

export default function Checkpoints() {
  const [checkpoints, setCheckpoints] = useState<Checkpoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [restoring, setRestoring] = useState<Checkpoint | null>(null);
  const [result, setResult] = useState<Result | null>(null);
  const tasks = useDesktop((s) => s.tasks);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await software.listCheckpoints({});
      setError("");
      setCheckpoints([...resp.checkpoints].sort((a, b) => millis(b) - millis(a)));
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const create = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      await software.createCheckpoint({ name: name.trim() });
      setName("");
      await refresh();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  }, [name, refresh]);

  const confirmRestore = useCallback(async () => {
    const cp = restoring;
    setRestoring(null);
    if (!cp) return;
    setBusy(true);
    setError("");
    try {
      const resp = await software.restoreCheckpoint({ id: cp.id });
      setResult({ name: cp.name || cp.id, notes: resp.notes, before: resp.before });
      await refresh();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  }, [restoring, refresh]);

  return (
    <div className="ckpt">
      <div className="ckpt__create">
        <input
          className="activity__search"
          type="text"
          placeholder="New checkpoint name (optional)"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !busy && void create()}
          aria-label="New checkpoint name"
        />
        <button className="tasks__btn tasks__btn--go" disabled={busy} onClick={() => void create()}>
          Take Checkpoint
        </button>
      </div>

      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      {result && (
        <div className="ckpt__result" role="status">
          <div className="ckpt__resulthead">
            <strong>Restored to {result.name}.</strong>
            <button className="svc__btn" onClick={() => setResult(null)}>
              Dismiss
            </button>
          </div>
          {result.before && <p className="ckpt__resultnote">A “{result.before.name || "Before Restore"}” checkpoint was taken first, so this Restore can be undone.</p>}
          {result.notes.length > 0 ? (
            <>
              <p className="ckpt__resultnote">Some things could not be restored exactly:</p>
              <ul className="ckpt__notes">
                {result.notes.map((n, i) => (
                  <li key={i}>{n}</li>
                ))}
              </ul>
            </>
          ) : (
            <p className="ckpt__resultnote">Everything was restored exactly.</p>
          )}
        </div>
      )}

      {loading && checkpoints.length === 0 ? (
        <div className="agent__empty">Loading…</div>
      ) : checkpoints.length === 0 ? (
        <div className="agent__empty">No checkpoints yet. Take one before a risky change.</div>
      ) : (
        <ul className="ckpt__list">
          {checkpoints.map((cp) => (
            <li key={cp.id} className="ckpt__item">
              <div className="ckpt__main">
                <div className="ckpt__row">
                  <span className="ckpt__name">{cp.name || "Checkpoint"}</span>
                  {cp.automatic && (
                    <span className="ckpt__auto" title="Taken automatically before a change or a Restore">
                      automatic
                    </span>
                  )}
                </div>
                <div className="ckpt__meta">
                  <span>{when(cp.createdAt, true)}</span>
                  <span> · up to ledger #{cp.ledgerId.toString()}</span>
                  {cp.taskId && <span title={cp.taskId}> · {tasks[cp.taskId] ? taskTitle(tasks[cp.taskId]) : cp.taskId}</span>}
                </div>
              </div>
              <button className="svc__btn" disabled={busy} onClick={() => setRestoring(cp)}>
                Restore
              </button>
            </li>
          ))}
        </ul>
      )}

      {restoring && (
        <Confirm
          title={`Restore to ${restoring.name || "this checkpoint"}?`}
          message="The Machine's software, /etc files and Services return to this checkpoint. A “Before Restore” checkpoint is taken first, so you can undo it."
          confirmLabel="Restore"
          onConfirm={() => void confirmRestore()}
          onCancel={() => setRestoring(null)}
        />
      )}
    </div>
  );
}

function millis(cp: Checkpoint): number {
  return cp.createdAt ? Number(cp.createdAt.seconds) * 1000 + Math.floor(cp.createdAt.nanos / 1e6) : 0;
}
