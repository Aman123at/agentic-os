// Terminal — xterm.js panes over aosd Sessions (PLAN.md §4.3, §10, M3.3). Each
// tab is a Session: "New" opens a User Session (unconfined, as aos) and "Watch"
// attaches read-only to an Agent's Session — a running one, or one whose Task
// ended recently, which replays what it showed. The xterm instances are owned by
// TermController and live outside React (§4.3 rule 6); this component only
// tracks which tabs exist and which one is showing.
import { useCallback, useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";

import { sessions } from "../api/client";
import type { SessionInfo } from "../gen/aos/v1/services_pb";
import { useDesktop } from "../store";
import { resolvedTheme } from "../theme";
import { TermController } from "./terminal/term";

interface Tab {
  id: string;
  agent: boolean;
  taskId: string;
  title: string;
  dead: boolean;
}

export default function Terminal() {
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [active, setActive] = useState("");
  const [error, setError] = useState("");
  const [watch, setWatch] = useState<SessionInfo[] | null>(null);
  const [busy, setBusy] = useState(false);
  const theme = useDesktop((s) => s.theme);
  const dark = resolvedTheme(theme) === "dark";

  const markDead = useCallback((id: string) => {
    setTabs((ts) => ts.map((t) => (t.id === id ? { ...t, dead: true } : t)));
  }, []);

  const newSession = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const resp = await sessions.createSession({ cols: 80, rows: 24 });
      const s = resp.session;
      if (!s) return;
      setTabs((ts) => [...ts, { id: s.id, agent: false, taskId: "", title: "Session", dead: false }]);
      setActive(s.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  // Open the very first tab automatically so the window is never empty.
  const bootstrapped = useRef(false);
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    void newSession();
  }, [newSession]);

  const openWatchMenu = useCallback(async () => {
    if (watch) {
      setWatch(null);
      return;
    }
    try {
      const resp = await sessions.listSessions({});
      // Running Sessions first, then the ones that ended recently.
      setWatch(resp.sessions.filter((s) => s.agent).sort((a, b) => Number(a.ended) - Number(b.ended)));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [watch]);

  const watchAgent = useCallback((s: SessionInfo) => {
    setWatch(null);
    setTabs((ts) => {
      if (ts.some((t) => t.id === s.id)) return ts; // already watching
      return [...ts, { id: s.id, agent: true, taskId: s.taskId, title: agentTitle(s), dead: false }];
    });
    setActive(s.id);
  }, []);

  const closeTab = useCallback(
    (id: string) => {
      setTabs((ts) => {
        const rest = ts.filter((t) => t.id !== id);
        setActive((cur) => (cur === id ? (rest.at(-1)?.id ?? "") : cur));
        return rest;
      });
    },
    [],
  );

  return (
    <div className="term" onClick={() => watch && setWatch(null)}>
      <div className="term__tabs">
        <div className="term__tabstrip">
          {tabs.map((t) => (
            <div
              key={t.id}
              className={`term__tab${t.id === active ? " term__tab--on" : ""}${t.dead ? " term__tab--dead" : ""}`}
              onClick={() => setActive(t.id)}
              title={t.agent ? `Watching Agent Session ${t.id}` : `Session ${t.id}`}
            >
              <span className="term__tab-icon">{t.agent ? "👁️" : "⌨️"}</span>
              <span className="term__tab-name">{t.title}</span>
              <button
                className="term__tab-close"
                title="Close tab"
                onClick={(e) => {
                  e.stopPropagation();
                  closeTab(t.id);
                }}
              >
                ✕
              </button>
            </div>
          ))}
        </div>
        <div className="term__actions">
          <div className="term__watch">
            <button className="term__btn" onClick={openWatchMenu} title="Watch an Agent Session (read-only)">
              👁️ Watch
            </button>
            {watch && (
              <div className="term__menu" onClick={(e) => e.stopPropagation()}>
                {watch.length === 0 ? (
                  <div className="term__menu-empty">No Agent Sessions to watch</div>
                ) : (
                  watch.map((s) => (
                    <button key={s.id} className="term__menu-item" onClick={() => watchAgent(s)}>
                      👁️ {agentTitle(s)}
                      {s.ended && <span className="term__menu-note"> · ended</span>}
                    </button>
                  ))
                )}
              </div>
            )}
          </div>
          <button className="term__btn" onClick={() => void newSession()} disabled={busy} title="New Session">
            ＋ New
          </button>
        </div>
      </div>

      {error && <div className="term__error">{error}</div>}

      <div className="term__stage">
        {tabs.length === 0 && !error && (
          <div className="term__empty">
            <p>No sessions open.</p>
            <button className="term__btn" onClick={() => void newSession()} disabled={busy}>
              ＋ New Session
            </button>
          </div>
        )}
        {tabs.map((t) => (
          <TermPane key={t.id} tab={t} active={t.id === active} dark={dark} onClose={() => markDead(t.id)} />
        ))}
      </div>
    </div>
  );
}

function agentTitle(s: SessionInfo): string {
  return s.taskId ? `Agent · ${s.taskId}` : "Agent Session";
}

// TermPane hosts one Session's xterm. The controller is created the first time
// the pane is shown (a hidden element has no size to fit) and then kept alive so
// its output keeps streaming while another tab is in front.
function TermPane({ tab, active, dark, onClose }: { tab: Tab; active: boolean; dark: boolean; onClose: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const ctrl = useRef<TermController | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!active) return;
    if (!ctrl.current && host.current) {
      const c = new TermController({
        id: tab.id,
        readOnly: tab.agent,
        dark,
        onClose: () => onCloseRef.current(),
      });
      c.mount(host.current);
      ctrl.current = c;
    } else {
      ctrl.current?.refit();
    }
  }, [active, tab.id, tab.agent, dark]);

  useEffect(() => {
    ctrl.current?.setTheme(dark);
  }, [dark]);

  useEffect(
    () => () => {
      ctrl.current?.dispose();
      ctrl.current = null;
    },
    [],
  );

  return (
    <div className="term__panewrap" style={{ display: active ? "block" : "none" }}>
      {tab.agent && <div className="term__readonly">read-only · watching</div>}
      <div ref={host} className="term__pane" />
    </div>
  );
}
