// Activity Monitor's Processes tab (PLAN.md §4.3, M4.4): every process in the
// Machine from SystemService.Processes, polled every 2 s, in a virtualised table
// so thousands of rows scroll like a few. Confined processes (Agent Sessions,
// their commands and the file workers) are marked, and rows an Agent started are
// tagged with their Task. Sorted by CPU, with a name filter.
import { useMemo, useState } from "react";

import { system } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { ProcessInfo } from "../../gen/aos/v1/services_pb";
import { useDesktop } from "../../store";
import { VirtualList } from "../../ui/VirtualList";
import { taskTitle } from "../agent/format";
import { bytes, percent } from "./format";
import { usePoll } from "./usePoll";

const ROW_HEIGHT = 26;
const POLL_MS = 2000;

export default function Processes() {
  const [procs, setProcs] = useState<ProcessInfo[]>([]);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const tasks = useDesktop((s) => s.tasks);

  usePoll(async (signal) => {
    try {
      const resp = await system.processes({}, { signal });
      setError("");
      setProcs(resp.processes);
    } catch (err) {
      if (!signal.aborted) setError(friendlyError(err));
    }
  }, POLL_MS);

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const matched = q ? procs.filter((p) => p.name.toLowerCase().includes(q) || p.command.toLowerCase().includes(q) || String(p.pid).includes(q)) : procs;
    return [...matched].sort((a, b) => (b.cpuKnown ? b.cpuPercent : -1) - (a.cpuKnown ? a.cpuPercent : -1) || Number(b.rssBytes - a.rssBytes));
  }, [procs, filter]);

  return (
    <div className="procs">
      <div className="activity__toolbar">
        <input className="activity__search" type="search" placeholder="Filter processes" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter processes" />
        <span className="activity__count">{rows.length} of {procs.length} processes</span>
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      <VirtualList
        className="procs__list"
        items={rows}
        rowHeight={ROW_HEIGHT}
        header={
          <div className="procs__row procs__row--head">
            <span>PID</span>
            <span>Name</span>
            <span>User</span>
            <span className="procs__num">CPU</span>
            <span className="procs__num">Memory</span>
            <span>Task</span>
          </div>
        }
        renderRow={(p, style) => (
          <div key={p.pid} style={style} className="procs__row" title={p.command}>
            <span className="procs__pid">{p.pid}</span>
            <span className="procs__name">
              {p.name}
              {p.confined && (
                <span className="procs__badge" title="Runs confined (no_new_privs), like every Agent process">
                  🔒
                </span>
              )}
            </span>
            <span>{p.user}</span>
            <span className="procs__num">{p.cpuKnown ? percent(p.cpuPercent) : "—"}</span>
            <span className="procs__num">{bytes(Number(p.rssBytes))}</span>
            <span className="procs__task" title={p.taskId}>
              {p.taskId ? (tasks[p.taskId] ? taskTitle(tasks[p.taskId]) : p.taskId) : "—"}
            </span>
          </div>
        )}
      />
    </div>
  );
}
