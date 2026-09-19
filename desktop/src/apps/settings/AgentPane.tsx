// The Agent pane (PLAN.md §4.5, M4.5): the model, reasoning effort, Autonomy and
// the limits that shape every Task. Model, effort, Autonomy and retries apply to
// Tasks created after the change; max Tasks and the Cost Limits take effect at
// once (a decision in the plan).
import { NumberSetting, SelectSetting, TextSetting } from "./Field";
import { useSettings } from "./useSettings";

// The display name for each reasoning effort; the wire values a model accepts
// come from the catalogue (M6.16), so this only translates them for the reader.
const EFFORT_NAME: Record<string, string> = {
  none: "None",
  minimal: "Minimal",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra high",
  max: "Max",
};

// The full wire enum, used when the model is unlisted (the catalogue is open).
const WIRE_EFFORTS = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];

const AUTONOMY = [
  { value: "auto", name: "Auto — act without asking" },
  { value: "confirm-risky", name: "Confirm risky actions" },
  { value: "confirm-all", name: "Confirm everything" },
];

// modelEfforts returns the reasoning efforts the chosen model accepts: its
// catalogue entry (by id or alias), or the full wire enum for an unlisted model.
function modelEfforts(models: { id: string; efforts: string[] }[], model: string): string[] {
  const entry = models.find((m) => m.id === model);
  return entry && entry.efforts.length > 0 ? entry.efforts : WIRE_EFFORTS;
}

export default function AgentPane() {
  const settings = useSettings();

  if (settings.loading) return <div className="agent__empty">Loading…</div>;

  const model = settings.byKey["model"]?.value ?? "";
  const modelList = settings.models.map((m) => ({ value: m.id, name: m.label || m.id }));
  const effortOptions = [
    { value: "", name: "Model default" },
    ...modelEfforts(settings.models, model).map((e) => ({ value: e, name: EFFORT_NAME[e] ?? e })),
  ];

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
        <TextSetting settings={settings} keyName="model" label="Model" hint="Pick one, or type any model your key can use" placeholder="gpt-5.6-terra" list={modelList} />
        <SelectSetting settings={settings} keyName="reasoning_effort" label="Reasoning effort" hint="Only the values this model accepts" options={effortOptions} />
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
