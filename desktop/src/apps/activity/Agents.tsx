// Activity Monitor's Agents tab (PLAN.md §4.3, M4.4): the Tasks that are running
// now, each with its Agent Session(s) and a button that opens the Terminal to
// Watch that Session read-only. Tasks come live from the store; Sessions are
// polled, since a Task's Session comes and goes as it works.
import { ConnectError } from "@connectrpc/connect";
import { useMemo, useState } from "react";

import { sessions as sessionApi } from "../../api/client";
import type { SessionInfo } from "../../gen/aos/v1/services_pb";
import { useDesktop } from "../../store";
import { isLive, shortCost, stateLabel, taskTitle, when } from "../agent/format";
import { usePoll } from "./usePoll";

const POLL_MS = 2000;

export default function Agents() {
  const tasksMap = useDesktop((s) => s.tasks);
  const openTaskView = useDesktop((s) => s.openTaskView);
  const watchInTerminal = useDesktop((s) => s.watchInTerminal);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [error, setError] = useState("");

  usePoll(async (signal) => {
    try {
      const resp = await sessionApi.listSessions({}, { signal });
      setError("");
      setSessions(resp.sessions.filter((s) => s.agent && !s.ended));
    } catch (err) {
      if (!signal.aborted) setError(ConnectError.from(err).message);
    }
  }, POLL_MS);

  const byTask = useMemo(() => {
    const m = new Map<string, SessionInfo[]>();
    for (const s of sessions) {
      const list = m.get(s.taskId) ?? [];
      list.push(s);
      m.set(s.taskId, list);
    }
    return m;
  }, [sessions]);

  const running = useMemo(() => Object.values(tasksMap).filter((t) => isLive(t.state)).sort((a, b) => millis(b) - millis(a)), [tasksMap]);

  return (
    <div className="agents">
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      {running.length === 0 ? (
        <div className="agent__empty">No Agents are running right now.</div>
      ) : (
        <ul className="agents__list">
          {running.map((t) => {
            const label = stateLabel(t.state);
            const cost = shortCost(t.usage);
            const its = byTask.get(t.id) ?? [];
            return (
              <li key={t.id} className="agents__item">
                <div className="agents__row">
                  <button className="agents__title" onClick={() => openTaskView(t.id)} title="Open in the Agent app">
                    {taskTitle(t)}
                  </button>
                  <span className={`tasks__state tasks__state--${label.mod}`}>{label.text}</span>
                </div>
                <div className="agents__meta">
                  <span>Started {when(t.createdAt, true)}</span>
                  {cost && <span>· {cost}</span>}
                </div>
                <div className="agents__sessions">
                  {its.length === 0 ? (
                    <span className="agents__nosession">No active Session</span>
                  ) : (
                    its.map((s) => (
                      <button key={s.id} className="agents__watch" onClick={() => watchInTerminal(s.id)} title="Watch this Session read-only in the Terminal">
                        👁️ Watch Session
                      </button>
                    ))
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function millis(t: { createdAt?: { seconds: bigint; nanos: number } }): number {
  return t.createdAt ? Number(t.createdAt.seconds) * 1000 + Math.floor(t.createdAt.nanos / 1e6) : 0;
}
