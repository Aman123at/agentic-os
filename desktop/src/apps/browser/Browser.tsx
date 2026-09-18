// Browser — a web page Chromium renders inside the Machine, streamed into this
// window (PLAN.md M5.2, ADR-0008). Only in Machines built with
// INCLUDE_BROWSER=true. The page itself lives in PageView, outside React; this
// component is the toolbar and the notices around it.
import { useEffect, useMemo, useRef, useState } from "react";

import { useWinMinimized, useWinState } from "../../shell/win";
import { Splitter } from "../../ui/Splitter";
import { taskTitle } from "../agent/format";
import { useDesktop } from "../../store";
import Bookmarks, { readMarks } from "./Bookmarks";
import { PageView, type PageState, type Status } from "./page";
import "./browser.css";

const BLANK = "about:blank";
// The bookmarks sidebar's width, kept with the window like the address; 0 is
// collapsed.
const SIDEBAR = { fallback: 180, min: 140, max: 380 };

export default function Browser() {
  const info = useDesktop((s) => s.info);
  if (!info?.browser) {
    return (
      <div className="browser browser--off">
        <p className="browser__emoji">🌐</p>
        <p>The Browser isn’t available in this Machine.</p>
        <p className="browser__hint">
          {info?.browserUnavailable || "Set INCLUDE_BROWSER=true in .env, then run docker compose up --build."}
        </p>
      </div>
    );
  }
  return <BrowserWindow />;
}

function BrowserWindow() {
  const canvas = useRef<HTMLCanvasElement>(null);
  const keys = useRef<HTMLTextAreaElement>(null);
  const address = useRef<HTMLInputElement>(null);
  const view = useRef<PageView | null>(null);
  const minimized = useWinMinimized();
  // The last address, kept with the window: a reload (or a browser that
  // stopped while idle) comes back to the same page.
  const [savedURL, setSavedURL] = useWinState("url", "");
  const saved = useRef(savedURL);
  saved.current = savedURL;
  // The bookmarks and the sidebar's width, kept with the window too.
  const [savedMarks, setMarks] = useWinState("marks", "[]");
  const marks = useMemo(() => readMarks(savedMarks), [savedMarks]);
  const [savedSide, setSidew] = useWinState("sidew", String(SIDEBAR.fallback));
  const sidew = Number(savedSide);

  const [state, setState] = useState<PageState>({ url: "", title: "", loading: false, canBack: false, canForward: false, agent: "" });
  const [status, setStatus] = useState<Status>("connecting");
  const [typed, setTyped] = useState<string | null>(null);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    let restored = false;
    const v = new PageView({
      onState: (s) => {
        setState(s);
        // A fresh browser is at about:blank; take it back to the saved page,
        // unless an Agent started it and is about to open its own.
        if (!restored) {
          restored = true;
          if (s.url === BLANK && !s.agent && saved.current && saved.current !== BLANK) v.send({ type: "navigate", url: saved.current });
        }
        if (s.url && s.url !== BLANK) setSavedURL(s.url);
      },
      onNotice: setNotice,
      onStatus: (st) => {
        setStatus(st);
        if (st === "connecting") restored = false;
      },
      onShortcut: (name) => {
        if (name === "address") {
          address.current?.focus();
          address.current?.select();
        } else {
          v.send({ type: name });
        }
      },
    });
    view.current = v;
    v.mount(canvas.current!, keys.current!);
    return () => {
      v.dispose();
      view.current = null;
    };
    // Once per window: setSavedURL is stable for the window's life.
  }, []);

  useEffect(() => {
    view.current?.setVisible(!minimized);
  }, [minimized]);

  // Notices fade on their own.
  useEffect(() => {
    if (!notice) return;
    const t = window.setTimeout(() => setNotice(""), 6000);
    return () => window.clearTimeout(t);
  }, [notice]);

  function go(e: React.FormEvent) {
    e.preventDefault();
    const url = (typed ?? state.url).trim();
    if (!url) return;
    address.current?.blur();
    open(url);
  }

  function open(url: string) {
    setNotice("");
    view.current?.send({ type: "navigate", url });
    setTyped(null);
    view.current?.focus();
  }

  function remove(url: string) {
    setMarks(JSON.stringify(marks.filter((b) => b.url !== url)));
  }

  const blank = !state.url || state.url === BLANK;
  const shown = typed ?? (blank ? "" : state.url);
  const starred = !blank && marks.some((b) => b.url === state.url);

  function star() {
    if (blank) return;
    if (starred) return remove(state.url);
    setMarks(JSON.stringify([...marks, { url: state.url, title: state.title }]));
    // Show where it went, if the sidebar is put away.
    if (sidew === 0) setSidew(String(SIDEBAR.fallback));
  }

  return (
    <div className="browser">
      <form className="browser__bar" onSubmit={go}>
        <button type="button" className="browser__btn" aria-label="Back" title="Back" disabled={!state.canBack} onClick={() => view.current?.send({ type: "back" })}>
          ‹
        </button>
        <button type="button" className="browser__btn" aria-label="Forward" title="Forward" disabled={!state.canForward} onClick={() => view.current?.send({ type: "forward" })}>
          ›
        </button>
        <button
          type="button"
          className="browser__btn"
          aria-label={state.loading ? "Stop" : "Reload"}
          title={state.loading ? "Stop" : "Reload"}
          disabled={status !== "open"}
          onClick={() => view.current?.send({ type: state.loading ? "stop" : "reload" })}
        >
          {state.loading ? "✕" : "↻"}
        </button>
        <button
          type="button"
          className={`browser__btn${starred ? " browser__btn--on" : ""}`}
          aria-label={starred ? "Remove bookmark" : "Bookmark this page"}
          aria-pressed={starred}
          title={starred ? "Remove bookmark" : "Bookmark this page"}
          disabled={blank}
          onClick={star}
        >
          {starred ? "★" : "☆"}
        </button>
        <button
          type="button"
          className="browser__btn"
          aria-label={sidew === 0 ? "Show bookmarks" : "Hide bookmarks"}
          aria-pressed={sidew !== 0}
          title={sidew === 0 ? "Show bookmarks" : "Hide bookmarks"}
          onClick={() => setSidew(String(sidew === 0 ? SIDEBAR.fallback : 0))}
        >
          ▤
        </button>
        <input
          ref={address}
          className="browser__address"
          aria-label="Address"
          placeholder="Search or enter an address"
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
          value={shown}
          title={state.title || undefined}
          onChange={(e) => setTyped(e.target.value)}
          onFocus={(e) => e.target.select()}
          onBlur={() => setTyped(null)}
          onKeyDown={(e) => {
            // Explicit rather than relying on implicit form submission, which
            // not every input path (automation, some IMEs) triggers.
            if (e.key === "Enter" && !e.nativeEvent.isComposing) {
              go(e);
            } else if (e.key === "Escape") {
              setTyped(null);
              e.currentTarget.blur();
            }
          }}
        />
        {state.loading && <span className="browser__progress" aria-hidden="true" />}
        {state.agent && <AgentBadge taskId={state.agent} />}
      </form>

      <div className="browser__body">
        <aside
          className={`browser__sidebar${sidew === 0 ? " browser__sidebar--off" : ""}`}
          style={{ width: sidew }}
          aria-label="Bookmarks"
        >
          <Bookmarks marks={marks} current={blank ? "" : state.url} onOpen={open} onRemove={remove} />
        </aside>
        <Splitter
          size={sidew}
          onSize={(w) => setSidew(String(w))}
          min={SIDEBAR.min}
          max={SIDEBAR.max}
          restore={SIDEBAR.fallback}
          label="Resize the bookmarks sidebar"
        />
        <div className="browser__viewport">
          <canvas ref={canvas} className="browser__page" aria-label={state.title || "Web page"} role="img" />
          <textarea ref={keys} className="browser__keys" aria-label="Page keyboard input" tabIndex={-1} />
          {blank && status === "open" && (
            <div className="browser__start">
              <p className="browser__emoji">🌐</p>
              <p>Search or enter an address above.</p>
              <p className="browser__hint">Pages open inside the Machine, so localhost reaches its own Services.</p>
            </div>
          )}
          {status === "connecting" && (
            <div className="browser__overlay" role="status">
              Starting the browser…
            </div>
          )}
          {status === "closed" && (
            <div className="browser__overlay" role="status">
              <p>The browser stopped.</p>
              <button className="browser__again" onClick={() => void view.current?.connect()}>
                Start it again
              </button>
            </div>
          )}
          {notice && (
            <div className="browser__notice" role="alert">
              {notice}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// AgentBadge says an Agent is using the page, and opens its Task.
function AgentBadge({ taskId }: { taskId: string }) {
  const task = useDesktop((s) => s.tasks[taskId]);
  const openTaskView = useDesktop((s) => s.openTaskView);
  const title = task ? taskTitle(task) : "a Task";
  return (
    <button
      type="button"
      className="browser__agent"
      title={`The Agent is using this page for “${title}”. Open the Task.`}
      onClick={() => openTaskView(taskId)}
    >
      🤖 Agent is browsing
    </button>
  );
}
