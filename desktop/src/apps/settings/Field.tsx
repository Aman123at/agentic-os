// The controls the Agent and Trash panes are built from: a labelled row that
// shows where a setting's value comes from (a default, an environment variable,
// or one saved here) with a Reset that clears the saved value so env or the
// default returns. Each control commits on blur or Enter, then reflects the
// value the server accepted (which may differ, e.g. "$1" becomes "1").
import { useEffect, useState, type ReactNode } from "react";

import type { Setting } from "../../gen/aos/v1/services_pb";
import type { Settings } from "./useSettings";

function Row({ settings, keyName, label, hint, children }: { settings: Settings; keyName: string; label: string; hint?: string; children: ReactNode }) {
  const s = settings.byKey[keyName];
  const saved = s?.source === "settings";
  return (
    <div className="set__row">
      <div className="set__label">
        <span className="set__name">{label}</span>
        {hint && <span className="set__hint">{hint}</span>}
      </div>
      <div className="set__control">
        {children}
        <div className="set__source">
          {saved ? (
            <>
              <span className="set__badge set__badge--saved">Saved</span>
              <button
                className="set__reset"
                disabled={settings.saving === keyName}
                title={s?.env ? `Back to ${s.env}, or the default` : "Back to the default"}
                onClick={() => void settings.update(keyName, "")}
              >
                Reset
              </button>
            </>
          ) : s?.source === "env" ? (
            <span className="set__badge" title={`Set by ${s.env}`}>
              {s.env}
            </span>
          ) : (
            <span className="set__badge">Default</span>
          )}
        </div>
      </div>
    </div>
  );
}

// TextSetting edits a free-text setting (the model name).
export function TextSetting({ settings, keyName, label, hint, placeholder }: { settings: Settings; keyName: string; label: string; hint?: string; placeholder?: string }) {
  const s = settings.byKey[keyName];
  const [draft, setDraft] = useState(s?.value ?? "");
  useEffect(() => setDraft(s?.value ?? ""), [s?.value]);
  const commit = () => {
    if (draft !== (s?.value ?? "")) void settings.update(keyName, draft);
  };
  return (
    <Row settings={settings} keyName={keyName} label={label} hint={hint}>
      <input
        className="set__input"
        type="text"
        value={draft}
        placeholder={placeholder}
        disabled={settings.saving === keyName}
        aria-label={label}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => e.key === "Enter" && commit()}
      />
    </Row>
  );
}

// NumberSetting edits a whole-number or money setting.
export function NumberSetting({ settings, keyName, label, hint, min, max, step, unit }: { settings: Settings; keyName: string; label: string; hint?: string; min?: number; max?: number; step?: number; unit?: string }) {
  const s = settings.byKey[keyName];
  const [draft, setDraft] = useState(s?.value ?? "");
  useEffect(() => setDraft(s?.value ?? ""), [s?.value]);
  const commit = () => {
    if (draft !== (s?.value ?? "")) void settings.update(keyName, draft);
  };
  return (
    <Row settings={settings} keyName={keyName} label={label} hint={hint}>
      <span className="set__num">
        <input
          className="set__input set__input--num"
          type="number"
          value={draft}
          min={min}
          max={max}
          step={step}
          disabled={settings.saving === keyName}
          aria-label={label}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => e.key === "Enter" && commit()}
        />
        {unit && <span className="set__unit">{unit}</span>}
      </span>
    </Row>
  );
}

// SelectSetting edits a setting with a fixed set of values (reasoning effort,
// Autonomy); it commits at once, since there is nothing to type.
export function SelectSetting({ settings, keyName, label, hint, options }: { settings: Settings; keyName: string; label: string; hint?: string; options: { value: string; name: string }[] }) {
  const s = settings.byKey[keyName];
  return (
    <Row settings={settings} keyName={keyName} label={label} hint={hint}>
      <select
        className="set__input set__select"
        value={s?.value ?? ""}
        disabled={settings.saving === keyName}
        aria-label={label}
        onChange={(e) => void settings.update(keyName, e.target.value)}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.name}
          </option>
        ))}
      </select>
    </Row>
  );
}

export type { Setting };
