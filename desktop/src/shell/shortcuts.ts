// The Desktop's keyboard shortcuts (PLAN.md §4.3), remappable in System Settings.
// A shortcut is a combo: the modifier flags plus the physical key (KeyboardEvent
// `code`, so it is layout-stable and matches whatever the user's keyboard
// prints). Combos are stored as a small string ("alt+Space", "alt+KeyW") in the
// Desktop's saved state, so every tab shares the remap; shell/keyboard.ts
// matches against them.
import { browserOS } from "./keyboard";

export type Action = "spotlight" | "closeWindow" | "switchWindow";

export type ShortcutMap = Record<Action, string>;

// The three remappable actions, in the order the Keyboard pane lists them.
export const ACTIONS: { id: Action; name: string; hint: string }[] = [
  { id: "spotlight", name: "Spotlight", hint: "Open the launcher" },
  { id: "closeWindow", name: "Close Window", hint: "Close the focused window" },
  { id: "switchWindow", name: "Switch Window", hint: "Cycle the open windows" },
];

// defaultShortcuts follows the user's own computer: only Spotlight differs
// (Ctrl+Space on Windows, Alt+Space elsewhere); Close and Switch use Alt everywhere.
export function defaultShortcuts(): ShortcutMap {
  return {
    spotlight: browserOS() === "win" ? "ctrl+Space" : "alt+Space",
    closeWindow: "alt+KeyW",
    switchWindow: "alt+Backquote",
  };
}

interface Combo {
  alt: boolean;
  ctrl: boolean;
  meta: boolean;
  shift: boolean;
  code: string;
}

const MODS = ["alt", "ctrl", "meta", "shift"] as const;

function parse(s: string): Combo | null {
  const parts = s.split("+");
  const code = parts.pop() ?? "";
  if (!code) return null;
  const set = new Set(parts);
  return { alt: set.has("alt"), ctrl: set.has("ctrl"), meta: set.has("meta"), shift: set.has("shift"), code };
}

function serialize(c: Combo): string {
  const mods = MODS.filter((m) => c[m]);
  return [...mods, c.code].join("+");
}

// comboFromEvent reads a keydown as a combo, or null while only modifiers are
// held (so the Keyboard pane can wait for the real key). At least one modifier
// is required, so a plain letter never becomes a global shortcut.
export function comboFromEvent(e: KeyboardEvent): string | null {
  if (["AltLeft", "AltRight", "ControlLeft", "ControlRight", "MetaLeft", "MetaRight", "ShiftLeft", "ShiftRight"].includes(e.code)) return null;
  if (!e.altKey && !e.ctrlKey && !e.metaKey) return null;
  return serialize({ alt: e.altKey, ctrl: e.ctrlKey, meta: e.metaKey, shift: e.shiftKey, code: e.code });
}

// matches reports whether a keydown fires the given combo.
export function matches(e: KeyboardEvent, combo: string): boolean {
  const c = parse(combo);
  return !!c && e.altKey === c.alt && e.ctrlKey === c.ctrl && e.metaKey === c.meta && e.shiftKey === c.shift && e.code === c.code;
}

const MOD_LABEL: Record<(typeof MODS)[number], string> = { alt: browserOS() === "mac" ? "⌥" : "Alt", ctrl: "Ctrl", meta: browserOS() === "mac" ? "⌘" : "Meta", shift: "⇧" };

// keyLabel turns a KeyboardEvent code into what the key prints: "KeyW" → "W",
// "Space" → "Space", "Backquote" → "`", "Digit1" → "1".
function keyLabel(code: string): string {
  if (code.startsWith("Key")) return code.slice(3);
  if (code.startsWith("Digit")) return code.slice(5);
  const named: Record<string, string> = { Space: "Space", Backquote: "`", Slash: "/", Backslash: "\\", Minus: "-", Equal: "=", Period: ".", Comma: ",", Semicolon: ";", Quote: "'", BracketLeft: "[", BracketRight: "]", Enter: "Enter", Escape: "Esc", Tab: "Tab" };
  return named[code] ?? code;
}

// comboLabel renders a combo the way a user reads it: "⌥Space", "Alt+W".
export function comboLabel(combo: string): string {
  const c = parse(combo);
  if (!c) return combo;
  const mac = browserOS() === "mac";
  const mods = MODS.filter((m) => c[m]).map((m) => MOD_LABEL[m]);
  const key = keyLabel(c.code);
  return mac ? [...mods, key].join("") : [...mods, key].join("+");
}
