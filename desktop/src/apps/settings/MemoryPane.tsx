// The Memory pane (PLAN.md §14, M4.5): what the Agent remembers across Tasks. An
// Agent's `remember` proposes an entry; it applies only once accepted here. You
// can also write your own, and forget any of them.
import { ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useState } from "react";

import { settings } from "../../api/client";
import type { Memory } from "../../gen/aos/v1/types_pb";
import { useDesktop } from "../../store";
import { taskTitle, when } from "../agent/format";

export default function MemoryPane() {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const tasks = useDesktop((s) => s.tasks);

  const refresh = useCallback(async () => {
    try {
      const resp = await settings.listMemory({});
      setMemories(resp.memories);
      setError("");
    } catch (err) {
      setError(ConnectError.from(err).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const run = useCallback(
    async (fn: () => Promise<unknown>) => {
      setBusy(true);
      setError("");
      try {
        await fn();
        await refresh();
      } catch (err) {
        setError(ConnectError.from(err).message);
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  const add = () => {
    const text = draft.trim();
    if (!text || busy) return;
    void run(async () => {
      await settings.addMemory({ text });
      setDraft("");
    });
  };

  const proposals = memories.filter((m) => m.status === "proposed");
  const accepted = memories.filter((m) => m.status !== "proposed");

  return (
    <div className="set__pane">
      <h2 className="set__title">Memory</h2>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <section className="set__group">
        <div className="apikey__set">
          <input
            className="set__input apikey__input"
            type="text"
            placeholder="Something the Agent should remember"
            value={draft}
            disabled={busy}
            aria-label="New memory"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
          />
          <button className="tasks__btn tasks__btn--go" disabled={busy || !draft.trim()} onClick={add}>
            Remember
          </button>
        </div>
      </section>

      {loading ? (
        <div className="agent__empty">Loading…</div>
      ) : (
        <>
          {proposals.length > 0 && (
            <section className="set__group">
              <h3 className="set__grouphead">Proposed by an Agent</h3>
              <ul className="mem__list">
                {proposals.map((m) => (
                  <li key={m.id} className="mem__item mem__item--proposed">
                    <div className="mem__main">
                      <p className="mem__text">{m.text}</p>
                      <div className="mem__meta">
                        <span>{when(m.createdAt, true)}</span>
                        {m.taskId && <span title={m.taskId}> · {tasks[m.taskId] ? taskTitle(tasks[m.taskId]) : m.taskId}</span>}
                      </div>
                    </div>
                    <div className="mem__actions">
                      <button className="tasks__btn tasks__btn--go" disabled={busy} onClick={() => void run(() => settings.acceptMemory({ id: m.id }))}>
                        Accept
                      </button>
                      <button className="svc__btn" disabled={busy} onClick={() => void run(() => settings.forgetMemory({ id: m.id }))}>
                        Dismiss
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          )}

          <section className="set__group">
            <h3 className="set__grouphead">Remembered</h3>
            {accepted.length === 0 ? (
              <div className="agent__empty">Nothing is remembered yet.</div>
            ) : (
              <ul className="mem__list">
                {accepted.map((m) => (
                  <li key={m.id} className="mem__item">
                    <div className="mem__main">
                      <p className="mem__text">{m.text}</p>
                      <div className="mem__meta">
                        <span>{when(m.createdAt, true)}</span>
                        {m.taskId && <span title={m.taskId}> · {tasks[m.taskId] ? taskTitle(tasks[m.taskId]) : m.taskId}</span>}
                      </div>
                    </div>
                    <button className="svc__btn" disabled={busy} onClick={() => void run(() => settings.forgetMemory({ id: m.id }))}>
                      Forget
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  );
}
