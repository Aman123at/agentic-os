// Software (PLAN.md §11, M4.4): the software the Machine has, the Install Ledger
// of every change with its before and after state, the Checkpoints you can
// restore to, and the progress of replaying the Ledger at startup. The view it
// shows is kept with its window, so a reload brings it back.
import { useWinState } from "../../shell/win";
import Checkpoints from "./Checkpoints";
import Ledger from "./Ledger";
import Packages from "./Packages";
import ReplayView from "./ReplayView";

const VIEWS = [
  { id: "packages", name: "Packages", icon: "📦" },
  { id: "ledger", name: "Install Ledger", icon: "📒" },
  { id: "checkpoints", name: "Checkpoints", icon: "⏱️" },
  { id: "replay", name: "Replay", icon: "🔁" },
] as const;

export default function Software() {
  const [view, setView] = useWinState("view", "packages");

  return (
    <div className="sw">
      <nav className="sw__nav" aria-label="Software views">
        {VIEWS.map((v) => (
          <button
            key={v.id}
            className={`sw__navitem${view === v.id ? " sw__navitem--on" : ""}`}
            aria-current={view === v.id ? "page" : undefined}
            onClick={() => setView(v.id)}
          >
            <span className="sw__navicon" aria-hidden="true">
              {v.icon}
            </span>
            {v.name}
          </button>
        ))}
      </nav>
      <div className="sw__main">
        {view === "ledger" ? <Ledger /> : view === "checkpoints" ? <Checkpoints /> : view === "replay" ? <ReplayView /> : <Packages />}
      </div>
    </div>
  );
}
