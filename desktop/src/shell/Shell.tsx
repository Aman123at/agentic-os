import { useEffect, useState, type MouseEvent as ReactMouseEvent } from "react";

import { wallpapers } from "../assets";
import { useDesktop } from "../store";
import { ContextMenu, MenuItem } from "../ui/ContextMenu";
import Approvals from "./Approvals";
import { SignInForm } from "./AuthScreens";
import Dock from "./Dock";
import MenuBar from "./MenuBar";
import NotificationCenter from "./NotificationCenter";
import Spotlight from "./Spotlight";
import WallpaperPicker from "./WallpaperPicker";
import Window from "./Window";
import { wallpaperVars } from "./wallpaper";
import { installShortcuts } from "./keyboard";
import { startWatchdog } from "./watchdog";

// Shell is the Desktop itself: wallpaper, the menu bar, the windows layer and the
// Dock (PLAN.md §4.3). Server state flows in through the store's event stream.
export default function Shell() {
  const windows = useDesktop((s) => s.windows);
  const wallpaper = useDesktop((s) => s.wallpaper);
  const glass = useDesktop((s) => s.glass);
  const openApp = useDesktop((s) => s.openApp);
  // The expiry modal: shown over the desktop when a live session lapsed, so the
  // windows are kept and re-auth resumes in place (PLAN.md §18 M6.5).
  const expired = useDesktop((s) => s.expired);
  const authError = useDesktop((s) => s.authError);
  const resumeSession = useDesktop((s) => s.resumeSession);
  // The remap in force; re-installing when it changes keeps every tab current.
  const shortcuts = useDesktop((s) => s.shortcuts);
  // The desktop's own right-click menu, and the wallpaper picker it opens.
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [picking, setPicking] = useState(false);

  // Right-clicking the desktop itself (not a window, the Dock or the menu bar)
  // opens our menu instead of the browser's.
  function onContextMenu(e: ReactMouseEvent) {
    if ((e.target as HTMLElement).closest(".window, .dock-wrap, .menubar")) return;
    e.preventDefault();
    setMenu({ x: e.clientX, y: e.clientY });
  }

  // A click anywhere, or losing focus, dismisses the menu — as the Finder's does.
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  // The frame watchdog runs only while Liquid Glass is on. If frames drop for a
  // sustained stretch it trips once: switch Glass off (which stops the watchdog
  // through this effect) and tell the user why (PLAN.md §22, M4.6).
  useEffect(() => {
    if (!glass) return;
    return startWatchdog(() => {
      const s = useDesktop.getState();
      s.setGlass(false);
      s.pushLocalNotification({
        title: "Liquid Glass turned off",
        body: "Frames were dropping, so the Desktop switched back to the plain panels.",
      });
    });
  }, [glass]);

  useEffect(() => {
    return installShortcuts(
      {
        spotlight: () => useDesktop.getState().toggleSpotlight(),
        closeWindow: () => {
          const s = useDesktop.getState();
          if (s.focused) s.closeWindow(s.focused);
        },
        // Cycling includes minimized windows, and focusing one restores it, so a
        // window can always be reached again from the keyboard.
        switchWindow: () => {
          const s = useDesktop.getState();
          const open = s.windows;
          if (open.length < 2) return;
          const i = open.findIndex((w) => w.id === s.focused);
          s.focusWindow(open[(i + 1) % open.length].id);
        },
      },
      shortcuts,
    );
  }, [shortcuts]);

  return (
    <div className="desktop" onContextMenu={onContextMenu}>
      {/* The gradient base paints instantly; the drawn wallpapers layer over it,
          the right one revealed by the theme (see index.css). "None" keeps just
          the gradient; a generated design paints its own, tuned by hue and
          saturation and darkened by the theme in CSS. */}
      {wallpaper === "aurora" && (
        <div className="wallpaper">
          <img className="wallpaper__art wallpaper__art--light" src={wallpapers.light} alt="" draggable={false} />
          <img className="wallpaper__art wallpaper__art--dark" src={wallpapers.dark} alt="" draggable={false} />
        </div>
      )}
      {typeof wallpaper === "object" && (
        <div className="wallpaper wp-gen" data-design={wallpaper.design} style={wallpaperVars(wallpaper.hue, wallpaper.sat)} />
      )}
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
      {menu && (
        <ContextMenu x={menu.x} y={menu.y}>
          <MenuItem
            label="Change Wallpaper…"
            onClick={() => {
              setMenu(null);
              setPicking(true);
            }}
          />
          <div className="menu__sep" />
          <MenuItem
            label="System Settings…"
            onClick={() => {
              setMenu(null);
              openApp("settings");
            }}
          />
        </ContextMenu>
      )}
      {picking && <WallpaperPicker onClose={() => setPicking(false)} />}
      {expired && (
        <div className="auth__overlay" role="dialog" aria-modal="true" aria-label="Session expired">
          <div className="boot__card auth__card">
            <p className="auth__title">Your session expired</p>
            <p className="boot__muted auth__hint">Sign in again to pick up where you left off. Your windows are still here.</p>
            <SignInForm onSubmit={resumeSession} error={authError} card={false} submitLabel="Resume" />
          </div>
        </div>
      )}
    </div>
  );
}
