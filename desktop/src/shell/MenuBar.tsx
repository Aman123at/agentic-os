import { useEffect, useState } from "react";

import { APPS } from "../apps/registry";
import { replayActive } from "../apps/software/replay";
import { useDesktop } from "../store";
import type { ThemePref } from "../theme";

const nextTheme: Record<ThemePref, ThemePref> = { auto: "light", light: "dark", dark: "auto" };
const themeLabel: Record<ThemePref, string> = { auto: "Auto", light: "Light", dark: "Dark" };

// MenuBar is the top strip: the AOS menu, the active app, and a small Control
// Center (connection, notifications, theme, clock) (PLAN.md §4.3).
export default function MenuBar() {
  const { openApp, setTheme } = useDesktop();
  const theme = useDesktop((s) => s.theme);
  const conn = useDesktop((s) => s.conn);
  const toggleNotifCenter = useDesktop((s) => s.toggleNotifCenter);
  // The bell badge counts what wants the user: pending Approvals first.
  const pending = useDesktop((s) => Object.keys(s.approvals).length);
  const notifications = useDesktop((s) => s.notifications.length);
  const badge = pending + notifications;
  const active = useDesktop((s) => {
    const win = s.windows.find((w) => w.id === s.focused && !w.minimized);
    return win ? APPS[win.appId]?.name : undefined;
  });
  // While the Install Ledger is replaying at startup (PLAN.md §11), the menu bar
  // shows its progress; clicking it opens Software.
  const replay = useDesktop((s) => s.replay);

  return (
    <div className="menubar">
      <div className="menubar__left">
        <button className="menubar__logo" title="About This Machine" onClick={() => openApp("about")}>
          ◆
        </button>
        <span className="menubar__app">{active ?? "Agentic OS"}</span>
      </div>
      <div className="menubar__right">
        {replayActive(replay) && (
          <button className="menubar__replay" title="Re-applying the Install Ledger — click to see progress" onClick={() => openApp("software")}>
            <span className="menubar__replay-spin" aria-hidden="true">
              🔁
            </span>
            Replaying{replay && replay.total > 0 ? ` ${replay.done}/${replay.total}` : "…"}
          </button>
        )}
        <span className={`menubar__conn menubar__conn--${conn}`} title={`aosd ${conn}`} />
        <button
          className={`menubar__item menubar__bell${pending > 0 ? " menubar__bell--alert" : ""}`}
          title={`${pending} pending approval(s), ${notifications} notification(s)`}
          onClick={() => toggleNotifCenter()}
        >
          🔔{badge > 0 ? ` ${badge}` : ""}
        </button>
        <button className="menubar__item" title="Appearance" onClick={() => setTheme(nextTheme[theme])}>
          {themeLabel[theme]}
        </button>
        <button className="menubar__item" title="System Settings" aria-label="System Settings" onClick={() => openApp("settings")}>
          ⚙️
        </button>
        <Clock />
      </div>
    </div>
  );
}

function Clock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 10_000);
    return () => clearInterval(t);
  }, []);
  const time = now.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  const day = now.toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });
  return <span className="menubar__clock">{day} {time}</span>;
}
