// The Trash pane (PLAN.md §12, M4.5): how long deleted files stay in the Trash
// and how much room they may take before the oldest are purged.
import { NumberSetting } from "./Field";
import { useSettings } from "./useSettings";

export default function TrashPane() {
  const settings = useSettings();

  if (settings.loading) return <div className="agent__empty">Loading…</div>;

  return (
    <div className="set__pane">
      <h2 className="set__title">Trash</h2>
      {settings.error && (
        <div className="tasks__error" role="alert">
          {settings.error}
        </div>
      )}

      <section className="set__group">
        <p className="set__note">A file in the Trash is removed for good once it is older than the retention, or when the Trash is over its size cap and it is among the oldest.</p>
        <NumberSetting settings={settings} keyName="trash_retention_days" label="Keep deleted files for" hint="1–3650" min={1} max={3650} step={1} unit="days" />
        <NumberSetting settings={settings} keyName="trash_max_gb" label="Size cap" hint="1–1024" min={1} max={1024} step={1} unit="GB" />
      </section>
    </div>
  );
}
