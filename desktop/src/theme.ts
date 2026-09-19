// Theme handling (PLAN.md §4.3): light and dark, following the user's own
// computer's prefers-color-scheme with a manual override. The palette itself lives in
// index.css as CSS custom properties keyed off the data-theme attribute.
export type ThemePref = "light" | "dark" | "auto";

export function applyTheme(pref: ThemePref): void {
  const root = document.documentElement;
  if (pref === "auto") {
    delete root.dataset.theme;
  } else {
    root.dataset.theme = pref;
  }
  repaintBackdrops();
}

// Chrome does not re-read what is behind a backdrop-filtered layer when only an
// ancestor's custom properties change, so the menu bar and the Dock kept the old
// palette until some unrelated repaint (PLAN.md M4.8 item 8.9). Dropping the
// filter for one frame tears those layers down and rebuilds them against the new
// theme; index.css turns backdrop-filter off while data-theming is set.
function repaintBackdrops(): void {
  const root = document.documentElement;
  root.dataset.theming = "";
  requestAnimationFrame(() => {
    requestAnimationFrame(() => delete root.dataset.theming);
  });
}

// resolvedTheme is what the user actually sees, following their own computer when "auto".
export function resolvedTheme(pref: ThemePref): "light" | "dark" {
  if (pref !== "auto") return pref;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

// Liquid Glass (PLAN.md §4.3, §22, M4.6): an optional, more translucent look for
// the menu bar, Dock and panels. It is a root attribute like the theme, so
// index.css styles it, and the frame watchdog can turn it off cheaply. Off by
// default; the CSS applies only when data-glass is set.
export function applyGlass(on: boolean): void {
  const root = document.documentElement;
  if (on) root.dataset.glass = "on";
  else delete root.dataset.glass;
}
