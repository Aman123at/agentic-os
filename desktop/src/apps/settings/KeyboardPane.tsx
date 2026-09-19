// The Keyboard pane (PLAN.md §4.3, M4.5): remap the three Desktop shortcuts, and
// turn on immersive mode. The remap is saved in the Desktop state, so every tab
// shares it (shell/keyboard.ts reads it). Immersive mode is per-tab: it goes
// Fullscreen and, where the browser supports it, holds the keys the browser
// would otherwise keep (Keyboard Lock), so Alt+Tab and the like reach AOS.
import { useCallback, useEffect, useState } from "react";

import { useDesktop } from "../../store";
import { ACTIONS, comboFromEvent, comboLabel, defaultShortcuts, type Action } from "../../shell/shortcuts";

interface KeyboardApi {
  lock?: (keys?: string[]) => Promise<void>;
  unlock?: () => void;
}

export default function KeyboardPane() {
  const shortcuts = useDesktop((s) => s.shortcuts);
  const setShortcut = useDesktop((s) => s.setShortcut);
  const [recording, setRecording] = useState<Action | null>(null);

  // While recording, the next real combo (a modifier plus a key) becomes the
  // shortcut. A capture-phase listener runs before the global one, so the key
  // being pressed doesn't also fire the shortcut it is replacing. Escape cancels.
  useEffect(() => {
    if (!recording) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.code === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setRecording(null);
        return;
      }
      const combo = comboFromEvent(e);
      e.preventDefault();
      e.stopPropagation();
      if (combo) {
        setShortcut(recording, combo);
        setRecording(null);
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [recording, setShortcut]);

  const restore = useCallback(() => {
    const d = defaultShortcuts();
    for (const a of ACTIONS) setShortcut(a.id, d[a.id]);
  }, [setShortcut]);

  return (
    <div className="set__pane">
      <h2 className="set__title">Keyboard</h2>

      <section className="set__group">
        <h3 className="set__grouphead">Shortcuts</h3>
        <p className="set__note">Every shortcut needs a modifier (⌥/Alt, Ctrl or ⌘). They are shared across your tabs.</p>
        {ACTIONS.map((a) => (
          <div className="set__row" key={a.id}>
            <div className="set__label">
              <span className="set__name">{a.name}</span>
              <span className="set__hint">{a.hint}</span>
            </div>
            <div className="set__control">
              <button
                className={`kbd__combo${recording === a.id ? " kbd__combo--rec" : ""}`}
                aria-label={`Shortcut for ${a.name}`}
                onClick={() => setRecording(recording === a.id ? null : a.id)}
              >
                {recording === a.id ? "Press keys… (Esc to cancel)" : comboLabel(shortcuts[a.id])}
              </button>
            </div>
          </div>
        ))}
        <button className="set__reset kbd__restore" onClick={restore}>
          Restore defaults
        </button>
      </section>

      <Immersive />
    </div>
  );
}

function Immersive() {
  const [on, setOn] = useState(() => !!document.fullscreenElement);
  const [error, setError] = useState("");

  useEffect(() => {
    const onChange = () => setOn(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", onChange);
    return () => document.removeEventListener("fullscreenchange", onChange);
  }, []);

  const enter = async () => {
    setError("");
    try {
      await document.documentElement.requestFullscreen();
      // Keyboard Lock (Chromium) keeps Alt+Tab, Esc and the like inside AOS.
      const kb = (navigator as Navigator & { keyboard?: KeyboardApi }).keyboard;
      if (kb?.lock) await kb.lock().catch(() => {});
    } catch (err) {
      setError(err instanceof Error ? err.message : "Fullscreen was refused.");
    }
  };

  const exit = async () => {
    const kb = (navigator as Navigator & { keyboard?: KeyboardApi }).keyboard;
    kb?.unlock?.();
    if (document.fullscreenElement) await document.exitFullscreen().catch(() => {});
  };

  return (
    <section className="set__group">
      <h3 className="set__grouphead">Immersive mode</h3>
      <p className="set__note">Fills the screen and, on Chromium, holds keys like Alt+Tab so they reach AOS instead of your computer.</p>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      <button className="tasks__btn tasks__btn--go" onClick={() => void (on ? exit() : enter())}>
        {on ? "Leave immersive mode" : "Enter immersive mode"}
      </button>
    </section>
  );
}
