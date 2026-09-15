// The Agent app (PLAN.md §4.3, M4.2): the Tasks, with their live step feeds and
// the controls to follow up, answer, cancel and resume; the Audit Log of every
// Tool call; and model Usage against the Cost Limits. Which view and which Task
// it shows are kept with its window, so a reload brings them back.
import { useWinState } from "../../shell/win";
import AuditLog from "./AuditLog";
import TasksView from "./TasksView";
import UsageView from "./UsageView";

const VIEWS = [
  { id: "tasks", name: "Tasks", icon: "🤖" },
  { id: "audit", name: "Audit Log", icon: "📜" },
  { id: "usage", name: "Usage", icon: "📊" },
] as const;

export default function Agent() {
  const [view, setView] = useWinState("view", "tasks");

  return (
    <div className="agent">
      <nav className="agent__nav" aria-label="Agent views">
        {VIEWS.map((v) => (
          <button
            key={v.id}
            className={`agent__navitem${view === v.id ? " agent__navitem--on" : ""}`}
            aria-current={view === v.id ? "page" : undefined}
            onClick={() => setView(v.id)}
          >
            <span className="agent__navicon" aria-hidden="true">
              {v.icon}
            </span>
            {v.name}
          </button>
        ))}
      </nav>
      <div className="agent__main">
        {view === "audit" ? <AuditLog /> : view === "usage" ? <UsageView /> : <TasksView />}
      </div>
    </div>
  );
}
