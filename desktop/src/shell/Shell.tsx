import { useEffect } from "react";

import { useDesktop } from "../store";
import Approvals from "./Approvals";
import Dock from "./Dock";
import MenuBar from "./MenuBar";
import NotificationCenter from "./NotificationCenter";
import Spotlight from "./Spotlight";
import Window from "./Window";
import { installShortcuts } from "./keyboard";

// Shell is the Desktop itself: wallpaper, the menu bar, the windows layer and the
// Dock (PLAN.md §4.3). Server state flows in through the store's event stream.
export default function Shell() {
  const windows = useDesktop((s) => s.windows);

  useEffect(() => {
    return installShortcuts({
      spotlight: () => useDesktop.getState().toggleSpotlight(),
      closeWindow: () => {
        const s = useDesktop.getState();
        if (s.focused) s.closeWindow(s.focused);
      },
      switchWindow: () => {
        const s = useDesktop.getState();
        const open = s.windows.filter((w) => !w.minimized);
        if (open.length < 2) return;
        const i = open.findIndex((w) => w.id === s.focused);
        s.focusWindow(open[(i + 1) % open.length].id);
      },
    });
  }, []);

  return (
    <div className="desktop">
      <div className="wallpaper" />
      <MenuBar />
      <div className="windows">
        {windows.map((win) => (
          <Window key={win.id} win={win} />
        ))}
      </div>
      <Dock />
      <Spotlight />
      <NotificationCenter />
      <Approvals />
    </div>
  );
}
