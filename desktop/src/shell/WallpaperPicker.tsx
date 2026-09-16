// The wallpaper picker (PLAN.md §4.3): a grid of designs — Aurora and None
// alongside the generated ones — with Hue and Saturation sliders that preview on
// the real Desktop as you drag. Done keeps the choice (already applied, and saved
// like every preference); Cancel restores what was set when it opened. It is used
// both from the desktop's right-click menu and inside System Settings.
import { useRef, useState } from "react";

import { useDesktop, type WallpaperPref } from "../store";
import { DEFAULT_HUE, DEFAULT_SAT, WALLPAPER_DESIGNS, wallpaperVars, type WallpaperDesignId } from "./wallpaper";

export default function WallpaperPicker({ onClose }: { onClose: () => void }) {
  const wallpaper = useDesktop((s) => s.wallpaper);
  const setWallpaper = useDesktop((s) => s.setWallpaper);
  // What was set when the picker opened, so Cancel can put it back exactly.
  const original = useRef(wallpaper);

  // The hue and saturation the sliders show. Seeded from a generated wallpaper,
  // or the defaults for Aurora/None, so picking a design starts somewhere sensible.
  const seed = typeof wallpaper === "object" ? wallpaper : { hue: DEFAULT_HUE, sat: DEFAULT_SAT };
  const [hue, setHue] = useState(seed.hue);
  const [sat, setSat] = useState(seed.sat);

  const selected = typeof wallpaper === "object" ? wallpaper.design : wallpaper;

  function pickDesign(design: WallpaperDesignId) {
    setWallpaper({ design, hue, sat });
  }
  function tune(next: { hue?: number; sat?: number }) {
    const h = next.hue ?? hue;
    const s = next.sat ?? sat;
    setHue(h);
    setSat(s);
    // The sliders only change a generated wallpaper; Aurora and None ignore them.
    if (typeof useDesktop.getState().wallpaper === "object") setWallpaper({ design: (useDesktop.getState().wallpaper as { design: WallpaperDesignId }).design, hue: h, sat: s });
  }

  return (
    <div className="wallpick__scrim" onClick={onClose} onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <div className="wallpick" role="dialog" aria-label="Wallpaper" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3 className="wallpick__title">Wallpaper</h3>

        <div className="wallpick__grid" role="radiogroup" aria-label="Wallpaper design">
          <Swatch name="Aurora" on={selected === "aurora"} onClick={() => setWallpaper("aurora")}>
            <span className="wallpick__preview appr__wall--aurora" aria-hidden="true" />
          </Swatch>
          <Swatch name="None" on={selected === "none"} onClick={() => setWallpaper("none")}>
            <span className="wallpick__preview appr__wall--none" aria-hidden="true" />
          </Swatch>
          {WALLPAPER_DESIGNS.map((d) => (
            <Swatch key={d.id} name={d.name} on={selected === d.id} onClick={() => pickDesign(d.id)}>
              <span className="wallpick__preview wp-gen" data-design={d.id} style={wallpaperVars(hue, sat)} aria-hidden="true" />
            </Swatch>
          ))}
        </div>

        <div className="wallpick__slider">
          <label htmlFor="wall-hue">Hue</label>
          <input id="wall-hue" type="range" min={0} max={360} value={hue} onChange={(e) => tune({ hue: Number(e.target.value) })} />
        </div>
        <div className="wallpick__slider">
          <label htmlFor="wall-sat">Saturation</label>
          <input id="wall-sat" type="range" min={0} max={100} value={sat} onChange={(e) => tune({ sat: Number(e.target.value) })} />
        </div>

        <div className="wallpick__actions">
          <button className="finder__btn" onClick={() => cancel(original.current, setWallpaper, onClose)}>
            Cancel
          </button>
          <button className="finder__btn finder__btn--on" onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </div>
  );
}

function cancel(original: WallpaperPref, setWallpaper: (p: WallpaperPref) => void, onClose: () => void) {
  setWallpaper(original);
  onClose();
}

function Swatch({ name, on, onClick, children }: { name: string; on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button className={`wallpick__swatch${on ? " wallpick__swatch--on" : ""}`} role="radio" aria-checked={on} onClick={onClick}>
      {children}
      <span className="wallpick__name">{name}</span>
    </button>
  );
}
