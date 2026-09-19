// Activity Monitor's Services & Ports tab (PLAN.md §4.3, §12, M4.4): the Services
// the Supervisor runs — start, stop, restart, and remove (after a confirmation)
// — with live logs through StreamLogs, and every listening port with a button
// that opens it. It refreshes on ServiceChanged events (the store's serviceEpoch)
// and on a light poll, so ports stay current.
import { useCallback, useMemo, useState } from "react";

import { openPort } from "../../api/ports";
import { supervisor } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { Listener, ServiceInfo } from "../../gen/aos/v1/types_pb";
import { useDesktop } from "../../store";
import { Confirm } from "../../ui/Confirm";
import { serviceState } from "./format";
import { LogPanel } from "./LogPanel";
import { usePoll } from "./usePoll";

const POLL_MS = 3000;

export default function Services() {
  const epoch = useDesktop((s) => s.serviceEpoch);
  const [services, setServices] = useState<ServiceInfo[]>([]);
  const [listeners, setListeners] = useState<Listener[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [logsFor, setLogsFor] = useState("");
  const [removing, setRemoving] = useState<ServiceInfo | null>(null);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
      const resp = await supervisor.listServices({}, signal ? { signal } : undefined);
      setError("");
      setServices(resp.services);
      setListeners(resp.listeners);
    } catch (err) {
      if (!signal?.aborted) setError(friendlyError(err));
    }
  }, []);

  usePoll((signal) => refresh(signal), POLL_MS, [epoch]);

  const act = useCallback(
    async (name: string, fn: () => Promise<unknown>) => {
      setBusy(name);
      setError("");
      try {
        await fn();
        await refresh();
      } catch (err) {
        setError(friendlyError(err));
      } finally {
        setBusy("");
      }
    },
    [refresh],
  );

  const confirmRemove = useCallback(async () => {
    const svc = removing;
    setRemoving(null);
    if (!svc) return;
    if (logsFor === svc.name) setLogsFor("");
    await act(svc.name, () => supervisor.removeService({ name: svc.name }));
  }, [removing, logsFor, act]);

  // Ports without a Service of their own, shown in their own list.
  const looseListeners = useMemo(() => listeners.filter((l) => !l.service), [listeners]);

  return (
    <div className="svc">
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <section className="svc__section">
        <h3 className="svc__h">Services</h3>
        {services.length === 0 ? (
          <p className="svc__empty">No Services yet. An Agent creates one when it runs a long-lived program.</p>
        ) : (
          <ul className="svc__list">
            {services.map((s) => {
              const st = serviceState(s.state);
              const running = st.mod === "running" || st.mod === "restarting";
              return (
                <li key={s.name} className="svc__item">
                  <div className="svc__main">
                    <div className="svc__row">
                      <span className={`svc__dot svc__dot--${st.mod}`} />
                      <span className="svc__name">{s.name}</span>
                      <span className={`svc__state svc__state--${st.mod}`}>{st.text}</span>
                      {s.root && <span className="svc__tag" title="Runs as root">root</span>}
                      {s.reachable && (
                        <span className="svc__tag svc__tag--exposed" title="Bound to 0.0.0.0 — reachable from the internet, past your Account">
                          exposed
                        </span>
                      )}
                      {s.ports.length > 0 && <span className="svc__ports">:{s.ports.join(", :")}</span>}
                    </div>
                    <div className="svc__meta">
                      <code className="svc__cmd" title={s.command}>{s.command}</code>
                      {s.restarts > 0 && <span> · {s.restarts} restart{s.restarts === 1 ? "" : "s"}</span>}
                      {s.lastExit && <span> · last {s.lastExit}</span>}
                    </div>
                  </div>
                  <div className="svc__actions">
                    {s.ports.map((p) => (
                      <button key={p} className="svc__btn" onClick={() => openPort(p)} title={`Open port ${p}`}>
                        Open :{p}
                      </button>
                    ))}
                    {running ? (
                      <button className="svc__btn" disabled={busy === s.name} onClick={() => void act(s.name, () => supervisor.stopService({ name: s.name }))}>
                        Stop
                      </button>
                    ) : (
                      <button className="svc__btn" disabled={busy === s.name} onClick={() => void act(s.name, () => supervisor.startService({ name: s.name }))}>
                        Start
                      </button>
                    )}
                    <button className="svc__btn" disabled={busy === s.name} onClick={() => void act(s.name, () => supervisor.restartService({ name: s.name }))}>
                      Restart
                    </button>
                    <button className={`svc__btn${logsFor === s.name ? " svc__btn--on" : ""}`} onClick={() => setLogsFor(logsFor === s.name ? "" : s.name)}>
                      Logs
                    </button>
                    <button className="svc__btn svc__btn--danger" disabled={busy === s.name} onClick={() => setRemoving(s)}>
                      Remove
                    </button>
                  </div>
                  {logsFor === s.name && <LogPanel name={s.name} onClose={() => setLogsFor("")} />}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="svc__section">
        <h3 className="svc__h">Other listening ports</h3>
        {looseListeners.length === 0 ? (
          <p className="svc__empty">Nothing else is listening on a port.</p>
        ) : (
          <ul className="svc__ports-list">
            {looseListeners.map((l) => (
              <li key={`${l.address}:${l.port}:${l.pid}`} className="svc__port">
                <span className="svc__portno">:{l.port}</span>
                <span className="svc__portproc">{l.process || "—"}</span>
                <span className="svc__portaddr">{l.address}</span>
                {l.reachable && (
                  <span className="svc__tag svc__tag--exposed" title="Bound to 0.0.0.0 — reachable from the internet, past your Account">
                    exposed
                  </span>
                )}
                <button className="svc__btn" onClick={() => openPort(l.port)}>
                  Open
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      {removing && (
        <Confirm
          title={`Remove ${removing.name}?`}
          message="The Service is stopped and removed. Anything it was serving stops. This cannot be undone."
          confirmLabel="Remove"
          danger
          onConfirm={() => void confirmRemove()}
          onCancel={() => setRemoving(null)}
        />
      )}
    </div>
  );
}
