// Loads the runtime settings (PLAN.md §6.4) and saves changes back. The Agent
// and Trash panes both read these; each gets the list on open and updates it in
// place, so a saved value's new source ("settings") and fallback come from the
// server, not a guess.
import { useCallback, useEffect, useState } from "react";

import { settings as settingsApi } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { ModelChoice, Setting } from "../../gen/aos/v1/services_pb";

export interface Settings {
  byKey: Record<string, Setting>;
  /** The model catalogue (M6.16): the models to offer and each one's accepted efforts. */
  models: ModelChoice[];
  loading: boolean;
  error: string;
  saving: string;
  /** Saves one setting; an empty value clears the saved one, so env or the default returns. Returns true on success. */
  update: (key: string, value: string) => Promise<boolean>;
}

export function useSettings(): Settings {
  const [byKey, setByKey] = useState<Record<string, Setting>>({});
  const [models, setModels] = useState<ModelChoice[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState("");

  useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const resp = await settingsApi.get({});
        if (!live) return;
        setByKey(Object.fromEntries(resp.settings.map((s) => [s.key, s])));
        setModels(resp.models);
        setError("");
      } catch (err) {
        if (live) setError(friendlyError(err));
      } finally {
        if (live) setLoading(false);
      }
    })();
    return () => {
      live = false;
    };
  }, []);

  const update = useCallback(async (key: string, value: string) => {
    setSaving(key);
    setError("");
    try {
      const resp = await settingsApi.update({ key, value });
      if (resp.setting) setByKey((m) => ({ ...m, [key]: resp.setting! }));
      return true;
    } catch (err) {
      setError(friendlyError(err));
      return false;
    } finally {
      setSaving("");
    }
  }, []);

  return { byKey, models, loading, error, saving, update };
}
