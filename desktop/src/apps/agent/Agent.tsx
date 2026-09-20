// The Agent app (PLAN.md §4.3, M4.2): the Tasks, with their live step feeds and
// the controls to follow up, answer, cancel and resume; the Audit Log of every
// Tool call; and model Usage against the Cost Limits. Which view and which Task
// it shows — and how wide its panes are — are kept with its window, so a reload
// brings them back.
import { useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { Splitter } from "../../ui/Splitter";
import AuditLog from "./AuditLog";
import TasksView from "./TasksView";
import UsageView from "./UsageView";

const VIEWS = [
  { id: "tasks", name: "Tasks", icon: "🤖" },
  { id: "audit", name: "Audit Log", icon: "📜" },
  { id: "usage", name: "Usage", icon: "📊" },
] as const;

const NAV = { fallback: 150, min: 110, max: 320 };

export default function Agent() {
  const [view, setView] = useWinState("view", "tasks");
  const [navw, setNavw] = useWinState("navw", String(NAV.fallback));
  const width = Number(navw);
  // In the Root Realm, Agents run as root and keep their own history, so the app
  // wears a banner saying so (M7.10).
  const rootMode = useDesktop((s) => !!s.info?.rootMode);

  return (
    <div className="agent__wrap">
      {rootMode && (
        <div className="agent__rootbanner">Root Mode — Agents run as root; this history is separate</div>
      )}
      <div className="agent">
      <nav
        className={`agent__nav${width === 0 ? " agent__nav--off" : ""}`}
        style={{ width }}
        aria-label="Agent views"
      >
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
      <Splitter
        size={width}
        onSize={(w) => setNavw(String(w))}
        min={NAV.min}
        max={NAV.max}
        restore={NAV.fallback}
        label="Resize the Agent views sidebar"
      />
      <div className="agent__main">
        {view === "audit" ? <AuditLog /> : view === "usage" ? <UsageView /> : <TasksView />}
      </div>
      </div>
    </div>
  );
}
