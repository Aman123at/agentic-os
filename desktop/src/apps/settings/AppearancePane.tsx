// The Appearance pane (PLAN.md §4.3, M4.5): the theme, the wallpaper and Liquid
// Glass (M4.6). All are saved in the Desktop state, so a new tab starts from
// them. Glass is off by default; the frame watchdog can turn it back off.
import { useState } from "react";

import { useDesktop } from "../../store";
import WallpaperPicker from "../../shell/WallpaperPicker";
import { WALLPAPER_DESIGNS } from "../../shell/wallpaper";
import type { ThemePref } from "../../theme";

const THEMES: { value: ThemePref; name: string; hint: string }[] = [
  { value: "auto", name: "Auto", hint: "Follow your computer" },
  { value: "light", name: "Light", hint: "" },
  { value: "dark", name: "Dark", hint: "" },
];

function wallpaperName(w: ReturnType<typeof useDesktop.getState>["wallpaper"]): string {
  if (w === "aurora") return "Aurora";
  if (w === "none") return "None";
  return WALLPAPER_DESIGNS.find((d) => d.id === w.design)?.name ?? "Custom";
}

export default function AppearancePane() {
  const theme = useDesktop((s) => s.theme);
  const setTheme = useDesktop((s) => s.setTheme);
  const wallpaper = useDesktop((s) => s.wallpaper);
  const glass = useDesktop((s) => s.glass);
  const setGlass = useDesktop((s) => s.setGlass);
  const [picking, setPicking] = useState(false);

  return (
    <div className="set__pane">
      <h2 className="set__title">Appearance</h2>

      <section className="set__group">
        <h3 className="set__grouphead">Theme</h3>
        <div className="appr__choices" role="radiogroup" aria-label="Theme">
          {THEMES.map((t) => (
            <button
              key={t.value}
              className={`appr__choice${theme === t.value ? " appr__choice--on" : ""}`}
              role="radio"
              aria-checked={theme === t.value}
              onClick={() => setTheme(t.value)}
            >
              <span className={`appr__swatch appr__swatch--${t.value}`} aria-hidden="true" />
              <span className="appr__cname">{t.name}</span>
              {t.hint && <span className="appr__chint">{t.hint}</span>}
            </button>
          ))}
        </div>
      </section>

      <section className="set__group">
        <h3 className="set__grouphead">Wallpaper</h3>
        <div className="set__row">
          <div className="set__label">
            <span className="set__name">Wallpaper</span>
            <span className="set__hint">Aurora, a plain background, or a generated design tuned by hue and saturation.</span>
          </div>
          <div className="set__control">
            <span className="set__value">{wallpaperName(wallpaper)}</span>
            <button className="finder__btn" onClick={() => setPicking(true)}>
              Choose…
            </button>
          </div>
        </div>
      </section>

      <section className="set__group">
        <h3 className="set__grouphead">Liquid Glass</h3>
        <div className="set__row">
          <div className="set__label">
            <span className="set__name">Liquid Glass</span>
            <span className="set__hint">A more translucent menu bar, Dock and panels. Needs a capable GPU; the Desktop turns it off if frames drop.</span>
          </div>
          <div className="set__control">
            <button
              className={`appr__switch${glass ? " appr__switch--on" : ""}`}
              role="switch"
              aria-checked={glass}
              aria-label="Liquid Glass"
              onClick={() => setGlass(!glass)}
            >
              <span className="appr__knob" aria-hidden="true" />
            </button>
          </div>
        </div>
      </section>
      {picking && <WallpaperPicker onClose={() => setPicking(false)} />}
    </div>
  );
}
