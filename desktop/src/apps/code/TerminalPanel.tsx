// Code's Terminal panel (PLAN.md M8.6): tabs of User Sessions below the
// editor, opened in the Workspace's folder. They are the Terminal app's
// Sessions — as aos in Standard Mode, as root in Root Mode with the red strip
// (M7.5, M7.10) — and the same TermController drives them, so their output
// never goes through React (§4.3 rule 6). Hiding the panel keeps them
// running; they end with the window. A reload takes the same shells back.
//
// The panel is its own lazy chunk: xterm loads the first time a Code window
// opens its Terminal, not with Code.
import { useContext, useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";

import { sessions } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { SessionInfo } from "../../gen/aos/v1/services_pb";
import { onWindowClosed } from "../../shell/closeGuard";
import { WinContext, useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { resolvedTheme } from "../../theme";
import { claim, claimed, release, TermController } from "../terminal/term";

interface Tab {
  id: string;
  n: number;
  dead: boolean;
  // Where the shell started, when that is not the Workspace: its user
  // cannot enter the folder.
  note: string;
}

export default function TerminalPanel({ root, visible, onHide }: { root: string; visible: boolean; onHide: () => void }) {
  const winId = useContext(WinContext);
  const rootMode = useDesktop((s) => Boolean(s.info?.rootMode));
  const dark = resolvedTheme(useDesktop((s) => s.theme)) === "dark";
  const [savedIds, setSavedIds] = useWinState("terms", "");
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [active, setActive] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const tabsRef = useRef<Tab[]>([]);
  tabsRef.current = tabs;
  const counter = useRef(0);
  const settled = useRef(false);

  const newSession = async () => {
    setBusy(true);
    setError("");
    try {
      const s = (await sessions.createSession({ cols: 80, rows: 24, cwd: root })).session;
      if (!s) return;
      claim(s.id);
      const note = s.cwd && s.cwd !== root ? `Started in ${s.cwd}: ${rootMode ? "root" : "aos"} can’t enter ${root}.` : "";
      const n = ++counter.current;
      setTabs((ts) => [...ts, { id: s.id, n, dead: false, note }]);
      setActive(s.id);
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  };

  // The panel first takes back the Sessions it had before a reload, and opens
  // a new one only when there are none.
  const bootstrapped = useRef(false);
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    void (async () => {
      let back: SessionInfo[] = [];
      const want = savedIds.split(",").filter(Boolean);
      if (want.length) {
        try {
          const alive = new Map((await sessions.listSessions({})).sessions.filter((x) => !x.agent && !x.ended && !claimed.has(x.id)).map((x) => [x.id, x]));
          back = want.map((id) => alive.get(id)).filter((x): x is SessionInfo => Boolean(x));
        } catch {
          // No listing: open a fresh one.
        }
      }
      settled.current = true;
      if (!back.length) return newSession();
      back.forEach((x) => claim(x.id));
      setTabs(back.map((x, i) => ({ id: x.id, n: i + 1, dead: false, note: "" })));
      counter.current = back.length;
      setActive(back[0].id);
    })();
  }, []);

  useEffect(() => {
    if (settled.current) setSavedIds(tabs.map((t) => t.id).join(","));
  }, [tabs, setSavedIds]);

  // Sessions end with the window. A reload or a remount only lets go of them,
  // for this window to take back.
  useEffect(() => {
    const off = onWindowClosed(winId, () => {
      for (const t of tabsRef.current) {
        release(t.id);
        void sessions.closeSession({ id: t.id }).catch(() => {});
      }
    });
    return () => {
      off();
      for (const t of tabsRef.current) release(t.id);
    };
  }, [winId]);

  const closeTab = (id: string) => {
    release(id);
    void sessions.closeSession({ id }).catch(() => {});
    const rest = tabs.filter((t) => t.id !== id);
    setTabs(rest);
    if (active === id) setActive(rest.at(-1)?.id ?? "");
    if (!rest.length) onHide();
  };

  const t = tabs.find((x) => x.id === active);
  return (
    <div className={`code-term${rootMode ? " term--root" : ""}`} role="region" aria-label="Terminal panel">
      {rootMode && <div className="term__rootbar">⛔ root — this Terminal runs as root</div>}
      <div className="code-term__bar">
        <div className="code-term__tabs" role="tablist" aria-label="Terminals">
          {tabs.map((x) => (
            <div
              key={x.id}
              role="tab"
              aria-selected={x.id === active}
              className={`code-term__tab${x.id === active ? " code-term__tab--on" : ""}${x.dead ? " code-term__tab--dead" : ""}`}
              onClick={() => setActive(x.id)}
              title={`Session ${x.id}`}
            >
              Terminal {x.n}
              {x.dead && <span className="term__tab-ended">ended</span>}
              <button
                className="code-term__close"
                aria-label={`Close Terminal ${x.n}`}
                onClick={(e) => {
                  e.stopPropagation();
                  closeTab(x.id);
                }}
              >
                ✕
              </button>
            </div>
          ))}
        </div>
        <button className="code__icon-btn" onClick={() => void newSession()} disabled={busy} title="New Terminal" aria-label="New Terminal">
          ＋
        </button>
        <button className="code__icon-btn" onClick={onHide} title="Hide the panel (⌃`)" aria-label="Hide the Terminal panel">
          ⌄
        </button>
      </div>
      {error && <div className="term__error">{error}</div>}
      {t?.note && <div className="code-term__note">{t.note}</div>}
      <div className="term__stage">
        {tabs.map((x) => (
          <Pane
            key={x.id}
            id={x.id}
            shown={visible && x.id === active}
            dark={dark}
            onEnd={() => setTabs((ts) => ts.map((y) => (y.id === x.id ? { ...y, dead: true } : y)))}
          />
        ))}
      </div>
    </div>
  );
}

// Pane is one Session's xterm, created the first time it is shown (a hidden
// element has no size to fit) and kept while other tabs are in front.
function Pane({ id, shown, dark, onEnd }: { id: string; shown: boolean; dark: boolean; onEnd: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const ctrl = useRef<TermController | null>(null);
  const onEndRef = useRef(onEnd);
  onEndRef.current = onEnd;

  useEffect(() => {
    if (!shown) return;
    if (!ctrl.current && host.current) {
      ctrl.current = new TermController({ id, readOnly: false, dark, onClose: () => onEndRef.current() });
      ctrl.current.mount(host.current);
    }
    // Once laid out: the panel may have only just been shown.
    requestAnimationFrame(() => ctrl.current?.refit());
  }, [shown, id]);

  useEffect(() => ctrl.current?.setTheme(dark), [dark]);
  useEffect(
    () => () => {
      ctrl.current?.dispose();
      ctrl.current = null;
    },
    [],
  );

  return (
    <div className="term__panewrap" style={{ display: shown ? "flex" : "none" }}>
      <div ref={host} className="term__pane" data-session={id} />
    </div>
  );
}
