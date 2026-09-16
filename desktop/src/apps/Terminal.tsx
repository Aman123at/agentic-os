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
import { useWinState } from "../shell/win";
import { useDesktop } from "../store";
import { resolvedTheme } from "../theme";
import { TermController } from "./terminal/term";

// The Sessions some Terminal window in this tab already shows. A second window
// re-attaches only to what is left, so two windows never type into one shell.
const claimed = new Set<string>();
const claim = (id: string) => claimed.add(id);
const release = (id: string) => claimed.delete(id);

// remember writes the window's User Sessions into its window state, so a reload
// re-attaches exactly these.
function remember(tabs: Tab[], save: (v: string) => void): void {
  save(tabs.filter((t) => !t.agent).map((t) => t.id).join(","));
}

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
  // The Sessions this window had, kept with the window and so restored with the
  // layout: a reload takes the same shells back instead of leaving them running
  // with no way to reach them (PLAN.md §10).
  const [savedIds, setSavedIds] = useWinState("sessions", "");
  const savedRef = useRef(savedIds);
  savedRef.current = savedIds;
  // True once the window has decided on its first tab, so the remembered list is
  // never cleared before the re-attach above has looked at it.
  const settled = useRef(false);
  // The tabs as the unmount cleanup sees them, without re-running it on change.
  const tabsRef = useRef<Tab[]>([]);
  tabsRef.current = tabs;

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
      claim(s.id);
      setTabs((ts) => [...ts, { id: s.id, agent: false, taskId: "", title: "Session", dead: false }]);
      setActive(s.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  // aosd keeps User Sessions running across a reload (PLAN.md §10), so this
  // window first takes back the Sessions it had — scrollback, folder and any
  // running command come with them. Only when it had none, or they are gone,
  // does it open a new one, so the window is never empty and no shell is left
  // running with nothing showing it.
  const bootstrapped = useRef(false);
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    void (async () => {
      setBusy(true);
      let back: SessionInfo[] = [];
      try {
        const want = savedRef.current.split(",").filter(Boolean);
        if (want.length > 0) {
          const resp = await sessions.listSessions({});
          const alive = new Map(resp.sessions.filter((x) => !x.agent && !x.ended && !claimed.has(x.id)).map((x) => [x.id, x]));
          back = want.map((id) => alive.get(id)).filter((x): x is SessionInfo => Boolean(x));
        }
      } catch {
        // No listing: fall through and open a fresh Session.
      } finally {
        setBusy(false);
      }
      if (back.length > 0) {
        for (const x of back) claim(x.id);
        const next = back.map((x) => ({ id: x.id, agent: false, taskId: "", title: "Session", dead: false }));
        settled.current = true;
        setTabs(next);
        setActive(next[0].id);
        return;
      }
      settled.current = true;
      await newSession();
    })();
  }, [newSession]);

  // Whatever the tabs end up being, the window remembers its User Sessions.
  useEffect(() => {
    if (settled.current) remember(tabs, setSavedIds);
  }, [tabs, setSavedIds]);

  const openWatchMenu = useCallback(async () => {
    if (watch) {
      setWatch(null);
      return;
    }
    try {
      const resp = await sessions.listSessions({});
      // Agent Sessions to watch, running ones first, then the ones that ended
      // recently — followed by any User Session no window is showing, which this
      // window can take back.
      const agents = resp.sessions.filter((x) => x.agent).sort((a, b) => Number(a.ended) - Number(b.ended));
      const loose = resp.sessions.filter((x) => !x.agent && !x.ended && !claimed.has(x.id));
      setWatch([...agents, ...loose]);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [watch]);

  const watchAgent = useCallback(
    (s: SessionInfo) => {
      setWatch(null);
      if (!s.agent) claim(s.id);
      setTabs((ts) => {
        if (ts.some((t) => t.id === s.id)) return ts; // already open here
        return [...ts, { id: s.id, agent: s.agent, taskId: s.taskId, title: s.agent ? agentTitle(s) : "Session", dead: false }];
      });
      setActive(s.id);
    },
    [],
  );

  const closeTab = useCallback(
    (id: string, agent: boolean) => {
      release(id);
      // Closing a User Session's tab ends its shell, as closing a Terminal tab
      // does on macOS; closing a watched Agent Session only stops watching.
      if (!agent) void sessions.closeSession({ id }).catch(() => {});
      setTabs((ts) => {
        const rest = ts.filter((t) => t.id !== id);
        setActive((cur) => (cur === id ? (rest.at(-1)?.id ?? "") : cur));
        return rest;
      });
    },
    [],
  );

  // Activity Monitor's Agents tab opens the Terminal to Watch one Session: it
  // sets watchSession, which we resolve to that Session and open read-only.
  const watchSession = useDesktop((s) => s.watchSession);
  const clearWatchSession = useDesktop((s) => s.clearWatchSession);
  useEffect(() => {
    if (!watchSession) return;
    let cancelled = false;
    void (async () => {
      try {
        const resp = await sessions.listSessions({});
        const s = resp.sessions.find((x) => x.id === watchSession);
        if (!cancelled && s) watchAgent(s);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!cancelled) clearWatchSession();
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [watchSession, watchAgent, clearWatchSession]);

  // While this window is mounted it owns its Sessions; when it goes away they are
  // free for the next Terminal window to take back.
  useEffect(() => {
    for (const t of tabsRef.current) if (!t.agent) claim(t.id);
    return () => {
      for (const t of tabsRef.current) release(t.id);
    };
  }, []);

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
                  closeTab(t.id, t.agent);
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
                  <div className="term__menu-empty">No Sessions to attach to</div>
                ) : (
                  watch.map((s) => (
                    <button key={s.id} className="term__menu-item" onClick={() => watchAgent(s)}>
                      {s.agent ? `👁️ ${agentTitle(s)}` : `⌨️ Session ${s.id}`}
                      {s.ended && <span className="term__menu-note"> · ended</span>}
                      {!s.agent && <span className="term__menu-note"> · re-attach</span>}
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
