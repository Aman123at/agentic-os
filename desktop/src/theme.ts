// Theme handling (PLAN.md §4.3): light and dark, following the Host's
// prefers-color-scheme with a manual override. The palette itself lives in
// index.css as CSS custom properties keyed off the data-theme attribute.
export type ThemePref = "light" | "dark" | "auto";

export function applyTheme(pref: ThemePref): void {
  const root = document.documentElement;
  if (pref === "auto") {
    delete root.dataset.theme;
  } else {
    root.dataset.theme = pref;
  }
}

// resolvedTheme is what the user actually sees, following the Host when "auto".
export function resolvedTheme(pref: ThemePref): "light" | "dark" {
  if (pref !== "auto") return pref;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
