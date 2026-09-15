// The Appearance pane (PLAN.md §4.3, M4.5): the theme and the wallpaper. Both are
// saved in the Desktop state, so a new tab starts from them. Liquid Glass joins
// this pane in M4.6.
import { useDesktop } from "../../store";
import type { WallpaperPref } from "../../store";
import type { ThemePref } from "../../theme";

const THEMES: { value: ThemePref; name: string; hint: string }[] = [
  { value: "auto", name: "Auto", hint: "Follow the Host" },
  { value: "light", name: "Light", hint: "" },
  { value: "dark", name: "Dark", hint: "" },
];

const WALLPAPERS: { value: WallpaperPref; name: string; hint: string }[] = [
  { value: "aurora", name: "Aurora", hint: "The drawn gradient" },
  { value: "none", name: "None", hint: "A plain background" },
];

export default function AppearancePane() {
  const theme = useDesktop((s) => s.theme);
  const setTheme = useDesktop((s) => s.setTheme);
  const wallpaper = useDesktop((s) => s.wallpaper);
  const setWallpaper = useDesktop((s) => s.setWallpaper);

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
        <div className="appr__choices" role="radiogroup" aria-label="Wallpaper">
          {WALLPAPERS.map((w) => (
            <button
              key={w.value}
              className={`appr__choice${wallpaper === w.value ? " appr__choice--on" : ""}`}
              role="radio"
              aria-checked={wallpaper === w.value}
              onClick={() => setWallpaper(w.value)}
            >
              <span className={`appr__wall appr__wall--${w.value}`} aria-hidden="true" />
              <span className="appr__cname">{w.name}</span>
              {w.hint && <span className="appr__chint">{w.hint}</span>}
            </button>
          ))}
        </div>
      </section>
    </div>
  );
}
