// Activity Monitor (PLAN.md §4.3, M4.4): four tabs over what the Machine is
// doing — the Processes table, the CPU/Memory/Disk/Network graphs, the running
// Agents, and Services & Ports. The tab it shows is kept with its window, so a
// reload brings it back. Each tab polls only while it is the one on screen.
import { useWinState } from "../../shell/win";
import Agents from "./Agents";
import Metrics from "./Metrics";
import Processes from "./Processes";
import Services from "./Services";

const TABS = [
  { id: "cpu", name: "CPU / Memory", icon: "📈" },
  { id: "processes", name: "Processes", icon: "🧮" },
  { id: "agents", name: "Agents", icon: "🤖" },
  { id: "services", name: "Services & Ports", icon: "🛰️" },
] as const;

export default function ActivityMonitor() {
  const [tab, setTab] = useWinState("tab", "cpu");

  return (
    <div className="activity">
      <nav className="activity__tabs" aria-label="Activity Monitor tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            className={`activity__tab${tab === t.id ? " activity__tab--on" : ""}`}
            aria-current={tab === t.id ? "page" : undefined}
            onClick={() => setTab(t.id)}
          >
            <span className="activity__tabicon" aria-hidden="true">
              {t.icon}
            </span>
            {t.name}
          </button>
        ))}
      </nav>
      <div className="activity__main">
        {tab === "processes" ? <Processes /> : tab === "agents" ? <Agents /> : tab === "services" ? <Services /> : <Metrics />}
      </div>
    </div>
  );
}
