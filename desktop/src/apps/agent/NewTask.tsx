// The New Task composer (PLAN.md §7): start a Task from the Agent app itself,
// the same as `aos run` — a prompt and, optionally, an Autonomy. Modelled on
// Finder's Ask dialog. ⌘/Ctrl-Enter starts it; Escape cancels. On submit the
// store selects the new Task, so the window lands straight in its live feed.
import { useEffect, useRef, useState } from "react";

import { Autonomy } from "../../gen/aos/v1/types_pb";
import { friendlyError } from "../../api/error";
import { useDesktop } from "../../store";

const AUTONOMY_OPTIONS: { value: Autonomy; label: string }[] = [
  { value: Autonomy.UNSPECIFIED, label: "Default (the configured Autonomy)" },
  { value: Autonomy.AUTO, label: "Auto — act without asking" },
  { value: Autonomy.CONFIRM_RISKY, label: "Confirm risky steps" },
  { value: Autonomy.CONFIRM_ALL, label: "Confirm every step" },
];

export default function NewTask({ onClose }: { onClose: () => void }) {
  const createTask = useDesktop((s) => s.createTask);
  const [text, setText] = useState("");
  const [autonomy, setAutonomy] = useState<Autonomy>(Autonomy.UNSPECIFIED);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);
  useEffect(() => ref.current?.focus(), []);

  async function submit() {
    const prompt = text.trim();
    if (!prompt || busy) return;
    setBusy(true);
    setError("");
    try {
      await createTask(prompt, autonomy);
      onClose();
    } catch (err) {
      setError(friendlyError(err));
      setBusy(false);
    }
  }

  return (
    <div className="quicklook" onClick={onClose}>
      <div className="ask" onClick={(e) => e.stopPropagation()} role="dialog" aria-label="New Task" aria-modal="true">
        <h3 className="ask__title">New Task</h3>
        <textarea
          ref={ref}
          className="ask__text"
          aria-label="What should the Agent do?"
          placeholder="Describe the Task… (⌘Enter to start)"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              void submit();
            }
          }}
        />
        <select className="agent__select ask__autonomy" aria-label="Autonomy" value={autonomy} onChange={(e) => setAutonomy(Number(e.target.value))}>
          {AUTONOMY_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        {error && (
          <div className="tasks__error" role="alert">
            {error}
          </div>
        )}
        <div className="ask__actions">
          <button className="finder__btn" onClick={onClose}>
            Cancel
          </button>
          <button className="finder__btn finder__btn--on" disabled={!text.trim() || busy} onClick={() => void submit()}>
            Start Task
          </button>
        </div>
      </div>
    </div>
  );
}
