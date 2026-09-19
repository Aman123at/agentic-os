// The API key pane (PLAN.md §7.7, M4.5): the OpenAI key the Agent uses. A key set
// here wins over the one from .env, across restarts, until "Use key from .env".
// Only a hint (sk-…abcd) of any key ever leaves aosd, so the field starts empty
// and the current key shows only as that hint.
import { useState } from "react";

import { settings } from "../../api/client";
import { friendlyError } from "../../api/error";
import { useDesktop } from "../../store";

export default function ApiKeyPane() {
  const info = useDesktop((s) => s.info);
  const setApiKeyInfo = useDesktop((s) => s.setApiKeyInfo);
  const [hint, setHint] = useState(info?.apiKeyHint ?? "");
  const [source, setSource] = useState(info?.apiKeySource ?? (info?.apiKey === "missing" ? "" : "env"));
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const set = async () => {
    const key = draft.trim();
    if (!key || busy) return;
    setBusy(true);
    setError("");
    try {
      const resp = await settings.setApiKey({ key });
      setHint(resp.hint);
      setSource("settings");
      setApiKeyInfo(resp.hint, "settings");
      setDraft("");
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  };

  const clear = async () => {
    setBusy(true);
    setError("");
    try {
      const resp = await settings.clearApiKey({});
      setHint(resp.hint);
      setSource(resp.hint ? "env" : "");
      setApiKeyInfo(resp.hint, resp.hint ? "env" : "");
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="set__pane">
      <h2 className="set__title">API key</h2>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <section className="set__group">
        <div className="apikey__now">
          {hint ? (
            <>
              <span className="apikey__hint">{hint}</span>
              <span className="set__badge" title={source === "settings" ? "Set in System Settings" : "From the .env secret"}>
                {source === "settings" ? "Set here" : "From .env"}
              </span>
            </>
          ) : (
            <span className="apikey__none">No key is set. The Agent cannot run Tasks until one is.</span>
          )}
        </div>

        <div className="apikey__set">
          <input
            className="set__input apikey__input"
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder="Paste a new key (sk-…)"
            value={draft}
            disabled={busy}
            aria-label="New API key"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && void set()}
          />
          <button className="tasks__btn tasks__btn--go" disabled={busy || !draft.trim()} onClick={() => void set()}>
            Replace key
          </button>
        </div>
        <p className="set__note">The key is sent once to aosd and stored on the Machine; it never comes back to the browser — only the hint above does.</p>

        {source === "settings" && (
          <button className="set__reset apikey__revert" disabled={busy} onClick={() => void clear()}>
            Use key from .env
          </button>
        )}
      </section>
    </div>
  );
}
