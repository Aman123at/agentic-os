// The Protected Paths pane (PLAN.md §7.3, M4.5): the paths the Agent may not
// write. The defaults (such as ~/.ssh) are read-only here — weakening them from a
// browser isn't worth the risk (a plan decision) — but you can lock your own
// paths and remove those again.
import { useCallback, useEffect, useState } from "react";

import { files } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { ListProtectedResponse_Entry as Entry } from "../../gen/aos/v1/services_pb";

export default function ProtectedPaths() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const resp = await files.listProtected({});
      setEntries(resp.entries);
      setError("");
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const add = useCallback(async () => {
    const path = draft.trim();
    if (!path || busy) return;
    setBusy(true);
    setError("");
    try {
      await files.protect({ path });
      setDraft("");
      await refresh();
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setBusy(false);
    }
  }, [draft, busy, refresh]);

  const remove = useCallback(
    async (path: string) => {
      setBusy(true);
      setError("");
      try {
        await files.unprotect({ path });
        await refresh();
      } catch (err) {
        setError(friendlyError(err));
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  return (
    <div className="set__pane">
      <h2 className="set__title">Protected Paths</h2>
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
            placeholder="/home/user/keep-safe"
            value={draft}
            disabled={busy}
            aria-label="Path to protect"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && void add()}
          />
          <button className="tasks__btn tasks__btn--go" disabled={busy || !draft.trim()} onClick={() => void add()}>
            Lock path
          </button>
        </div>

        {loading ? (
          <div className="agent__empty">Loading…</div>
        ) : entries.length === 0 ? (
          <div className="agent__empty">Nothing is protected yet.</div>
        ) : (
          <ul className="prot__list">
            {entries.map((e) => (
              <li key={`${e.source}:${e.path}`} className="prot__item">
                <div className="prot__main">
                  <code className="prot__path">{e.path}</code>
                  <div className="prot__meta">
                    <span className="set__badge">{sourceLabel(e.source)}</span>
                    <span className="prot__enf" title={e.kernel ? "Enforced by the kernel (Landlock)" : "Checked by policy"}>
                      {e.kernel ? "🛡️ kernel" : "policy"}
                    </span>
                  </div>
                </div>
                {e.source === "user" ? (
                  <button className="svc__btn" disabled={busy} onClick={() => void remove(e.path)}>
                    Remove
                  </button>
                ) : (
                  <span className="prot__locked" title="A built-in protection; it cannot be removed here">
                    read-only
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function sourceLabel(source: string): string {
  return source === "user" ? "Yours" : source === "pattern" ? "Pattern" : "Default";
}
