// System Settings (PLAN.md §4.3, M4.5): the panes that configure the running
// Machine — the Agent's model and limits, the API key, Protected Paths, Memory,
// Trash, the keyboard and appearance, and a read-only Status. The open pane is
// kept with the window, so a reload brings it back.
import { useWinState } from "../../shell/win";
import AgentPane from "./AgentPane";
import ApiKeyPane from "./ApiKeyPane";
import AppearancePane from "./AppearancePane";
import KeyboardPane from "./KeyboardPane";
import MemoryPane from "./MemoryPane";
import ProtectedPaths from "./ProtectedPaths";
import StatusPane from "./StatusPane";
import SystemPane from "./SystemPane";
import TrashPane from "./TrashPane";

const PANES = [
  { id: "system", name: "System", icon: "⚙️", Component: SystemPane },
  { id: "agent", name: "Agent", icon: "🤖", Component: AgentPane },
  { id: "apikey", name: "API key", icon: "🔑", Component: ApiKeyPane },
  { id: "protected", name: "Protected Paths", icon: "🛡️", Component: ProtectedPaths },
  { id: "memory", name: "Memory", icon: "🧠", Component: MemoryPane },
  { id: "trash", name: "Trash", icon: "🗑️", Component: TrashPane },
  { id: "keyboard", name: "Keyboard", icon: "⌨️", Component: KeyboardPane },
  { id: "appearance", name: "Appearance", icon: "🎨", Component: AppearancePane },
  { id: "status", name: "Status", icon: "ℹ️", Component: StatusPane },
] as const;

export default function Settings() {
  const [pane, setPane] = useWinState("pane", "agent");
  const current = PANES.find((p) => p.id === pane) ?? PANES[0];
  const Pane = current.Component;

  return (
    <div className="set">
      <nav className="set__nav" aria-label="Settings panes">
        {PANES.map((p) => (
          <button
            key={p.id}
            className={`set__navitem${current.id === p.id ? " set__navitem--on" : ""}`}
            aria-current={current.id === p.id ? "page" : undefined}
            onClick={() => setPane(p.id)}
          >
            <span className="set__navicon" aria-hidden="true">
              {p.icon}
            </span>
            {p.name}
          </button>
        ))}
      </nav>
      <div className="set__main">
        <Pane />
      </div>
    </div>
  );
}
