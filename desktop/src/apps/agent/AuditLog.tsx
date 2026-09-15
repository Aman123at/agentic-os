// The Agent app's Audit Log view (PLAN.md §7.5): every Tool call and what
// decided it, newest first, from SystemService.Audit. It pages as you scroll,
// renders only the rows in view, and filters to one Task. Picking a row shows
// its arguments (secrets already redacted by aosd) and result.
import { ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { system } from "../../api/client";
import type { AuditEntry } from "../../gen/aos/v1/types_pb";
import { useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { VirtualList } from "../../ui/VirtualList";
import { millis, taskTitle, when } from "./format";

const PAGE = 200;
const ROW_HEIGHT = 28;

interface Page {
  entries: AuditEntry[];
  end: boolean; // the oldest entry is loaded
  loading: boolean;
}

export default function AuditLog() {
  const [taskId, setTaskId] = useWinState("auditTask", "");
  const tasks = useDesktop((s) => s.tasks);
  const [page, setPage] = useState<Page>({ entries: [], end: false, loading: true });
  const [error, setError] = useState("");
  const [picked, setPicked] = useState<bigint | null>(null);
  // The latest page and filter, for the scroll handler; seq drops answers to an
  // older filter.
  const current = useRef(page);
  current.current = page;
  const seq = useRef(0);

  const fetchPage = useCallback(
    async (fresh: boolean) => {
      const p = current.current;
      if (!fresh && (p.loading || p.end)) return;
      const mine = fresh ? ++seq.current : seq.current;
      const beforeId = fresh ? 0n : (p.entries.at(-1)?.id ?? 0n);
      setPage((old) => ({ ...old, loading: true }));
      current.current = { ...p, loading: true };
      try {
        const resp = await system.audit({ taskId, limit: PAGE, beforeId });
        if (mine !== seq.current) return;
        setError("");
        setPage((old) => ({
          entries: fresh ? resp.entries : [...old.entries, ...resp.entries],
          end: resp.entries.length < PAGE,
          loading: false,
        }));
      } catch (err) {
        if (mine !== seq.current) return;
        setError(ConnectError.from(err).message);
        setPage((old) => ({ ...old, loading: false }));
      }
    },
    [taskId],
  );

  useEffect(() => {
    setPicked(null);
    void fetchPage(true);
  }, [fetchPage]);

  const taskOptions = useMemo(() => Object.values(tasks).sort((a, b) => millis(b.createdAt) - millis(a.createdAt)), [tasks]);
  const entry = picked === null ? undefined : page.entries.find((e) => e.id === picked);

  return (
    <div className="audit">
      <div className="agent__toolbar">
        <select className="agent__select" aria-label="Filter by Task" value={taskId} onChange={(e) => setTaskId(e.target.value)}>
          <option value="">All Tasks</option>
          {taskId && !tasks[taskId] && <option value={taskId}>{taskId}</option>}
          {taskOptions.map((t) => (
            <option key={t.id} value={t.id}>
              {taskTitle(t)}
            </option>
          ))}
        </select>
        <span className="agent__count">{page.loading ? "Loading…" : `${page.entries.length}${page.end ? "" : "+"} entries`}</span>
        <button className="tasks__btn agent__refresh" onClick={() => void fetchPage(true)}>
          Refresh
        </button>
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      {!page.loading && page.entries.length === 0 && !error ? (
        <div className="agent__empty">Nothing in the Audit Log yet.</div>
      ) : (
        <VirtualList
          className="audit__list"
          items={page.entries}
          rowHeight={ROW_HEIGHT}
          onEndReached={() => void fetchPage(false)}
          header={
            <div className="audit__row audit__row--head">
              <span>Time</span>
              <span>Task</span>
              <span>Tool</span>
              <span>Decision</span>
              <span>Decided by</span>
              <span>Result</span>
            </div>
          }
          renderRow={(e, style) => (
            <button
              key={e.id.toString()}
              style={style}
              className={`audit__row${e.id === picked ? " audit__row--on" : ""}`}
              onClick={() => setPicked(e.id === picked ? null : e.id)}
            >
              <span>{when(e.time, true)}</span>
              <span title={e.taskId}>{!e.taskId ? "—" : tasks[e.taskId] ? taskTitle(tasks[e.taskId]) : e.taskId}</span>
              <span className="audit__tool">{e.tool}</span>
              <span className={`audit__decision audit__decision--${e.decision}`}>{e.decision}</span>
              <span>{e.decidedBy}</span>
              <span title={e.resultSummary}>{e.resultSummary}</span>
            </button>
          )}
        />
      )}
      {entry && (
        <div className="audit__detail">
          <div className="audit__detailhead">
            <code>{entry.tool}</code> · {entry.decision} by {entry.decidedBy || "—"} · {entry.durationMs.toString()} ms
            {entry.actor && ` · ${entry.actor}`}
          </div>
          {entry.argumentsJson && <pre className="audit__args">{pretty(entry.argumentsJson)}</pre>}
          {entry.resultSummary && <div className="audit__result">{entry.resultSummary}</div>}
        </div>
      )}
    </div>
  );
}

function pretty(json: string): string {
  try {
    return JSON.stringify(JSON.parse(json), null, 2);
  } catch {
    return json;
  }
}
