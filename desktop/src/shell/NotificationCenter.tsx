// The Notification Center (PLAN.md §4.3, M3.4): a dropdown from the menu bar
// bell that gathers what wants the user's attention — pending Approvals, live
// and finished Tasks, and the notifications aosd has pushed. Everything here is
// read from the store's event-fed state; clicking a row opens that Task.
import { useMemo } from "react";

import { TaskState } from "../gen/aos/v1/types_pb";
import { useDesktop } from "../store";
import { isLive, stateLabel, taskTitle } from "../apps/tasks/format";

// A port opens through aosd's forwarding at <port>.localhost (PLAN.md §12), in a
// new tab from this click, which browsers allow where a pushed pop-up is blocked.
function openPort(port: number) {
  const { protocol, port: hostPort } = window.location;
  window.open(`${protocol}//${port}.localhost${hostPort ? `:${hostPort}` : ""}/`, "_blank", "noopener");
}

export default function NotificationCenter() {
  const open = useDesktop((s) => s.notifCenter);
  const toggle = useDesktop((s) => s.toggleNotifCenter);
  const openTaskView = useDesktop((s) => s.openTaskView);
  const approvals = useDesktop((s) => s.approvals);
  const tasks = useDesktop((s) => s.tasks);
  const notifications = useDesktop((s) => s.notifications);
  const dismiss = useDesktop((s) => s.dismissNotification);

  const pending = useMemo(() => Object.values(approvals), [approvals]);
  const taskList = useMemo(
    () =>
      Object.values(tasks)
        .sort((a, b) => Number(b.updatedAt?.seconds ?? 0n) - Number(a.updatedAt?.seconds ?? 0n))
        .slice(0, 8),
    [tasks],
  );

  if (!open) return null;

  const empty = pending.length === 0 && taskList.length === 0 && notifications.length === 0;

  return (
    <>
      <div className="nc-scrim" onClick={() => toggle(false)} />
      <div className="nc" role="dialog" aria-label="Notification Center">
        {empty && <div className="nc__empty">Nothing yet.</div>}

        {pending.length > 0 && (
          <section className="nc__section">
            <h3 className="nc__title">Needs you</h3>
            {pending.map((a) => (
              <button key={a.id} className="nc__row nc__row--await" onClick={() => openTaskView(a.taskId)}>
                <span className="nc__icon">🔐</span>
                <span className="nc__text">{a.summary}</span>
              </button>
            ))}
          </section>
        )}

        {taskList.length > 0 && (
          <section className="nc__section">
            <h3 className="nc__title">Tasks</h3>
            {taskList.map((t) => {
              const st = stateLabel(t.state);
              return (
                <button key={t.id} className="nc__row" onClick={() => openTaskView(t.id)}>
                  <span className="nc__icon">{isLive(t.state) ? "⏳" : t.state === TaskState.SUCCEEDED ? "✅" : "•"}</span>
                  <span className="nc__text">{taskTitle(t)}</span>
                  <span className={`nc__state nc__state--${st.mod}`}>{st.text}</span>
                </button>
              );
            })}
          </section>
        )}

        {notifications.length > 0 && (
          <section className="nc__section">
            <h3 className="nc__title nc__title--row">
              Notifications
              <button className="nc__clear" onClick={() => dismiss()}>
                Clear all
              </button>
            </h3>
            {notifications.slice(0, 12).map((n) => (
              <div key={n.id} className="nc__item">
                <button className="nc__row" onClick={() => n.taskId && openTaskView(n.taskId)} disabled={!n.taskId}>
                  <span className="nc__icon">🔔</span>
                  <span className="nc__text">
                    <strong>{n.title}</strong>
                    {n.body ? ` — ${n.body}` : ""}
                  </span>
                </button>
                {n.port > 0 && (
                  <button className="nc__open" onClick={() => openPort(n.port)}>
                    Open
                  </button>
                )}
                <button className="nc__dismiss" aria-label={`Dismiss ${n.title}`} onClick={() => dismiss(n.id)}>
                  ×
                </button>
              </div>
            ))}
          </section>
        )}
      </div>
    </>
  );
}
