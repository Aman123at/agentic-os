// The System pane (PLAN.md §18 M7.8, M7.9): the first pane in System Settings,
// holding the Root Mode switch and Restart AOS. Both restart aosd out of band
// (M7.3) and are confirmed by watching Info's boot_id change, then reloading —
// the refresh token signs back in silently and sessionStorage restores the
// windows (M6.5).
import { useEffect, useMemo, useRef, useState } from "react";

import { ConnectError } from "@connectrpc/connect";

import { system } from "../../api/client";
import { friendlyError } from "../../api/error";
import { useDesktop } from "../../store";
import { RootModeAttemptSchema, RootModeBlockedSchema, RootModeLockoutSchema } from "../../gen/aos/v1/services_pb";
import { type Task, TaskState } from "../../gen/aos/v1/types_pb";

// TaskRef is the little a notice needs of a blocking Task: it comes either from
// the live store (Task) or from the server's FailedPrecondition detail
// (BlockingTask), which share these fields.
type TaskRef = { id: string; title: string; state: TaskState };

// The states that count as "an Agent is still working" and block a switch (M7.7).
function activeTasks(tasks: Record<string, Task>): Task[] {
  return Object.values(tasks)
    .filter((t) => t.state === TaskState.QUEUED || t.state === TaskState.RUNNING || t.state === TaskState.AWAITING_USER)
    .sort((a, b) => a.id.localeCompare(b.id));
}

function stateLabel(s: TaskState): string {
  switch (s) {
    case TaskState.QUEUED:
      return "Queued";
    case TaskState.RUNNING:
      return "Running";
    case TaskState.AWAITING_USER:
      return "Awaiting you";
    default:
      return "";
  }
}

// flow is the modal sequence the pane is in.
type Flow =
  | { kind: "idle" }
  | { kind: "blocked"; tasks: TaskRef[]; goingTo: boolean }
  | { kind: "warn" } // entering Root Mode: the red warning gate
  | { kind: "password" } // entering Root Mode: the password modal
  | { kind: "offConfirm" } // leaving Root Mode: one lighter confirm
  | { kind: "restartConfirm" } // Restart AOS
  | { kind: "working"; label: string; fromBootId: string }; // the restart/switch overlay

export default function SystemPane() {
  const info = useDesktop((s) => s.info);
  const tasks = useDesktop((s) => s.tasks);
  const openTaskView = useDesktop((s) => s.openTaskView);
  const rootMode = !!info?.rootMode;
  const [flow, setFlow] = useState<Flow>({ kind: "idle" });

  // The switch and both confirms read the live Task list, not the boot-time Info
  // snapshot, so a Task that starts after the pane loads still blocks the switch.
  const blocking = useMemo(() => activeTasks(tasks), [tasks]);

  // beginSwitch opens the flow to turn Root Mode on or off, unless an Agent is
  // still working — then the switch springs back and the notice names the Tasks.
  const beginSwitch = (goingTo: boolean) => {
    if (blocking.length > 0) {
      setFlow({ kind: "blocked", tasks: blocking, goingTo });
      return;
    }
    setFlow(goingTo ? { kind: "warn" } : { kind: "offConfirm" });
  };

  const startWorking = (label: string) => setFlow({ kind: "working", label, fromBootId: info?.bootId ?? "" });

  // onServerBlocked handles a switch the server refused because a Task started
  // while the modals were open (FailedPrecondition, M7.7): the same notice.
  const onServerBlocked = (tasks: TaskRef[], goingTo: boolean) => setFlow({ kind: "blocked", tasks, goingTo });

  return (
    <div className="set__pane">
      <h2 className="set__title">System</h2>

      <section className="set__group">
        <div className="set__grouphead">Root Mode</div>
        <div className="set__row">
          <div className="set__label">
            <span className="set__name">Start in Root Mode</span>
            <span className="set__hint">
              Agents and the Terminal run as root with full <code>sudo</code>; Protected Paths are not enforced. AOS restarts to switch.
            </span>
          </div>
          <div className="set__control">
            <button
              type="button"
              role="switch"
              aria-checked={rootMode}
              aria-label="Start in Root Mode"
              className={`appr__switch${rootMode ? " appr__switch--on rootmode__switch--on" : ""}`}
              onClick={() => beginSwitch(!rootMode)}
            >
              <span className="appr__knob" />
            </button>
          </div>
        </div>
      </section>

      <section className="set__group">
        <div className="set__grouphead">Restart</div>
        <div className="set__row">
          <div className="set__label">
            <span className="set__name">Restart AOS</span>
            <span className="set__hint">Running Tasks will stop and be marked interrupted; open Terminal sessions and Services will restart.</span>
          </div>
          <div className="set__control">
            <button className="tasks__btn" onClick={() => setFlow({ kind: "restartConfirm" })}>
              Restart AOS
            </button>
          </div>
        </div>
      </section>

      {flow.kind === "blocked" && (
        <BlockedNotice tasks={flow.tasks} onClose={() => setFlow({ kind: "idle" })} onOpen={(id) => openTaskView(id)} />
      )}
      {flow.kind === "warn" && <WarnModal onCancel={() => setFlow({ kind: "idle" })} onContinue={() => setFlow({ kind: "password" })} />}
      {flow.kind === "password" && (
        <PasswordModal
          onCancel={() => setFlow({ kind: "idle" })}
          onSwitched={() => startWorking("Switching to Root Mode…")}
          onBlocked={(tasks) => onServerBlocked(tasks, true)}
        />
      )}
      {flow.kind === "offConfirm" && (
        <OffConfirmModal
          onCancel={() => setFlow({ kind: "idle" })}
          onSwitched={() => startWorking("Switching to Standard Mode…")}
          onBlocked={(tasks) => onServerBlocked(tasks, false)}
        />
      )}
      {flow.kind === "restartConfirm" && (
        <RestartConfirmModal onCancel={() => setFlow({ kind: "idle" })} onRestarting={() => startWorking("Restarting AOS…")} />
      )}
      {flow.kind === "working" && <WorkingOverlay label={flow.label} fromBootId={flow.fromBootId} />}
    </div>
  );
}

// BlockedNotice names the Tasks that must finish or be cancelled first, each with
// an Open link into the Agent app (M7.9).
function BlockedNotice({ tasks, onClose, onOpen }: { tasks: TaskRef[]; onClose: () => void; onOpen: (id: string) => void }) {
  return (
    <div className="rootmode__notice" role="alert" data-testid="rootmode-blocked">
      <p className="rootmode__noticetitle">An Agent is still running — Root Mode can’t be switched until it finishes or you cancel it.</p>
      <ul className="rootmode__tasks">
        {tasks.map((t) => (
          <li key={t.id} className="rootmode__task">
            <span className="rootmode__taskname">{t.title || t.id}</span>
            <span className="set__badge">{stateLabel(t.state)}</span>
            <button className="set__reset" onClick={() => onOpen(t.id)}>
              Open
            </button>
          </li>
        ))}
      </ul>
      <div className="rootmode__actions">
        <button className="tasks__btn" onClick={onClose}>
          OK
        </button>
      </div>
    </div>
  );
}

function Overlay({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="auth__overlay" role="dialog" aria-modal="true" aria-label={label}>
      <div className="boot__card auth__card rootmode__modal">{children}</div>
    </div>
  );
}

// WarnModal is the red gate before Root Mode: Continue stays disabled until the
// person acknowledges that these actions cannot be undone (M7.9).
function WarnModal({ onCancel, onContinue }: { onCancel: () => void; onContinue: () => void }) {
  const [ack, setAck] = useState(false);
  return (
    <Overlay label="Turn on Root Mode">
      <p className="auth__title rootmode__warntitle">Turn on Root Mode?</p>
      <ul className="rootmode__warnlist">
        <li>Agents and the Terminal will run as root with full <code>sudo</code>.</li>
        <li>Every file and folder is unlocked; Protected Paths are not enforced.</li>
        <li>Changes to the system <strong>cannot be undone</strong> by AOS — Trash and Checkpoints don’t cover what root does outside them.</li>
        <li>Root Mode keeps its own chats and Audit Log, hidden from Standard Mode and back.</li>
        <li>Open Terminal sessions and Services will stop, and AOS will restart.</li>
        <li>A root Agent can, in the end, get around AOS itself.</li>
      </ul>
      <label className="rootmode__ack">
        <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
        <span>I understand these actions cannot be undone</span>
      </label>
      <div className="rootmode__actions">
        <button className="tasks__btn" onClick={onCancel}>
          Cancel
        </button>
        <button className="tasks__btn tasks__btn--danger" disabled={!ack} onClick={onContinue}>
          Continue
        </button>
      </div>
    </Overlay>
  );
}

// PasswordModal asks for the Desktop account's password to enter Root Mode. A
// wrong password clears the field and shows the tries left; a lockout disables it
// with the unlock time; a Task that started meanwhile is surfaced as the notice.
function PasswordModal({
  onCancel,
  onSwitched,
  onBlocked,
}: {
  onCancel: () => void;
  onSwitched: () => void;
  onBlocked: (tasks: TaskRef[]) => void;
}) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [lockedUntil, setLockedUntil] = useState("");
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);

  const submit = async () => {
    if (busy || lockedUntil) return;
    setBusy(true);
    setError("");
    try {
      await system.setRootMode({ enabled: true, password });
      onSwitched();
    } catch (err) {
      const ce = ConnectError.from(err);
      const blocked = ce.findDetails(RootModeBlockedSchema)[0];
      if (blocked) {
        onBlocked(blocked.tasks);
        return;
      }
      const lock = ce.findDetails(RootModeLockoutSchema)[0];
      if (lock) {
        setLockedUntil(untilText(lock.until));
        setPassword("");
        setError("");
        return;
      }
      const attempt = ce.findDetails(RootModeAttemptSchema)[0];
      if (attempt) {
        setError(`Incorrect password — ${attempt.attemptsLeft} attempt${attempt.attemptsLeft === 1 ? "" : "s"} left`);
        setPassword("");
        input.current?.focus();
        return;
      }
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Overlay label="Enter your password">
      <p className="auth__title">Enter your password to turn on Root Mode</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <input
          ref={input}
          className="set__input rootmode__pw"
          type="password"
          autoComplete="current-password"
          aria-label="Password"
          value={password}
          disabled={!!lockedUntil}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error && (
          <p className="tasks__error" role="alert">
            {error}
          </p>
        )}
        {lockedUntil && (
          <p className="tasks__error" role="alert">
            Too many attempts — try again at {lockedUntil}
          </p>
        )}
        <div className="rootmode__actions">
          <button type="button" className="tasks__btn" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="tasks__btn tasks__btn--go" disabled={busy || !!lockedUntil || !password}>
            Turn on Root Mode
          </button>
        </div>
      </form>
    </Overlay>
  );
}

// OffConfirmModal leaves Root Mode: one lighter confirm, no password (M7.9).
function OffConfirmModal({
  onCancel,
  onSwitched,
  onBlocked,
}: {
  onCancel: () => void;
  onSwitched: () => void;
  onBlocked: (tasks: TaskRef[]) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const off = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await system.setRootMode({ enabled: false, password: "" });
      onSwitched();
    } catch (err) {
      const blocked = ConnectError.from(err).findDetails(RootModeBlockedSchema)[0];
      if (blocked) {
        onBlocked(blocked.tasks);
        return;
      }
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Overlay label="Turn off Root Mode">
      <p className="auth__title">Turn off Root Mode?</p>
      <p className="boot__muted rootmode__confirmtext">Root Mode Services will stop; AOS will restart into Standard Mode.</p>
      {error && (
        <p className="tasks__error" role="alert">
          {error}
        </p>
      )}
      <div className="rootmode__actions">
        <button className="tasks__btn" onClick={onCancel}>
          Cancel
        </button>
        <button className="tasks__btn tasks__btn--danger" disabled={busy} onClick={() => void off()}>
          Turn off Root Mode
        </button>
      </div>
    </Overlay>
  );
}

// RestartConfirmModal is the Restart AOS confirm (M7.8).
function RestartConfirmModal({ onCancel, onRestarting }: { onCancel: () => void; onRestarting: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const restart = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await system.restart({});
      onRestarting();
    } catch (err) {
      setError(friendlyError(err));
      setBusy(false);
    }
  };
  return (
    <Overlay label="Restart AOS">
      <p className="auth__title">Restart AOS?</p>
      <p className="boot__muted rootmode__confirmtext">Running Tasks will stop and be marked interrupted.</p>
      {error && (
        <p className="tasks__error" role="alert">
          {error}
        </p>
      )}
      <div className="rootmode__actions">
        <button className="tasks__btn" onClick={onCancel}>
          Cancel
        </button>
        <button className="tasks__btn tasks__btn--danger" disabled={busy} onClick={() => void restart()}>
          Restart
        </button>
      </div>
    </Overlay>
  );
}

// WorkingOverlay covers the screen while aosd restarts, polling Info until the
// boot_id changes and then reloading the page (M7.8). A 60 s timeout offers Try
// again and points at the daemon logs.
function WorkingOverlay({ label, fromBootId }: { label: string; fromBootId: string }) {
  const [timedOut, setTimedOut] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let stopped = false;
    const start = Date.now();
    // The e2e suite runs against a faked aosd that answers at once, so the poll
    // and the timeout are shortened there to keep the specs fast (M7.8 tests).
    const e2e = !!(window as unknown as { __AOS_E2E__?: boolean }).__AOS_E2E__;
    const timeoutMs = e2e ? 6_000 : 60_000;
    const intervalMs = e2e ? 400 : 1_000;
    const tick = async () => {
      if (stopped) return;
      if (Date.now() - start > timeoutMs) {
        setTimedOut(true);
        return;
      }
      try {
        const info = await system.info({});
        if (info.bootId && info.bootId !== fromBootId) {
          window.location.reload();
          return;
        }
      } catch {
        // aosd is down mid-restart; keep polling until it answers again.
      }
      if (!stopped) window.setTimeout(tick, intervalMs);
    };
    const id = window.setTimeout(tick, intervalMs);
    return () => {
      stopped = true;
      window.clearTimeout(id);
    };
  }, [fromBootId, attempt]);

  return (
    <div className="auth__overlay" role="dialog" aria-modal="true" aria-label={label} data-testid="rootmode-working">
      <div className="boot__card">
        {!timedOut ? (
          <>
            <p className="boot__logo">{label}</p>
            <p className="boot__muted">This takes a few seconds.</p>
          </>
        ) : (
          <>
            <p className="boot__logo">AOS is taking a while to come back</p>
            <p className="boot__muted">
              Check <code>sudo aos daemon logs</code> if it does not return.
            </p>
            <div className="rootmode__actions rootmode__actions--center">
              <button
                className="tasks__btn tasks__btn--go"
                onClick={() => {
                  setTimedOut(false);
                  setAttempt((n) => n + 1);
                }}
              >
                Try again
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// untilText formats a lockout's unlock time (a protobuf Timestamp) as HH:MM.
function untilText(until: { seconds: bigint } | undefined): string {
  if (!until) return "";
  return new Date(Number(until.seconds) * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
