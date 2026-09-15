// The Agent pane (PLAN.md §4.5, M4.5): the model, reasoning effort, Autonomy and
// the limits that shape every Task. Model, effort, Autonomy and retries apply to
// Tasks created after the change; max Tasks and the Cost Limits take effect at
// once (a decision in the plan).
import { NumberSetting, SelectSetting, TextSetting } from "./Field";
import { useSettings } from "./useSettings";

const EFFORT = [
  { value: "none", name: "None" },
  { value: "minimal", name: "Minimal" },
  { value: "low", name: "Low" },
  { value: "medium", name: "Medium" },
  { value: "high", name: "High" },
  { value: "xhigh", name: "Extra high" },
];

const AUTONOMY = [
  { value: "auto", name: "Auto — act without asking" },
  { value: "confirm-risky", name: "Confirm risky actions" },
  { value: "confirm-all", name: "Confirm everything" },
];

export default function AgentPane() {
  const settings = useSettings();

  if (settings.loading) return <div className="agent__empty">Loading…</div>;

  return (
    <div className="set__pane">
      <h2 className="set__title">Agent</h2>
      {settings.error && (
        <div className="tasks__error" role="alert">
          {settings.error}
        </div>
      )}

      <section className="set__group">
        <h3 className="set__grouphead">Model</h3>
        <TextSetting settings={settings} keyName="model" label="Model" hint="The model every Task runs on" placeholder="gpt-5.6-terra" />
        <SelectSetting settings={settings} keyName="reasoning_effort" label="Reasoning effort" hint="How hard the model thinks" options={EFFORT} />
      </section>

      <section className="set__group">
        <h3 className="set__grouphead">Autonomy</h3>
        <SelectSetting settings={settings} keyName="autonomy" label="Autonomy" hint="Applies to the next Task" options={AUTONOMY} />
        <NumberSetting settings={settings} keyName="max_tasks" label="Max Tasks at once" hint="1–16, effective immediately" min={1} max={16} step={1} />
        <NumberSetting settings={settings} keyName="max_retries" label="Max retries" hint="0–20 per step" min={0} max={20} step={1} />
      </section>

      <section className="set__group">
        <h3 className="set__grouphead">Cost Limits</h3>
        <p className="set__note">A Task stops when it reaches its limit. 0 means no limit.</p>
        <NumberSetting settings={settings} keyName="task_cost_limit_usd" label="Per Task" min={0} step={0.1} unit="USD" />
        <NumberSetting settings={settings} keyName="daily_cost_limit_usd" label="Per day" min={0} step={0.5} unit="USD" />
      </section>
    </div>
  );
}
