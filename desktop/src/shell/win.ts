// WinContext tells an app which window it is showing in, so it can keep a
// little state with that window (saved with the layout, restored on reload) and
// know when its window has the focus.
import { createContext, useCallback, useContext } from "react";

import { useDesktop } from "../store";

export const WinContext = createContext("");

// useWinState is a string kept with the app's window, like useState.
export function useWinState(key: string, fallback: string): [string, (value: string) => void] {
  const id = useContext(WinContext);
  const value = useDesktop((s) => s.windows.find((w) => w.id === id)?.state?.[key]);
  const setWinState = useDesktop((s) => s.setWinState);
  const setValue = useCallback((v: string) => setWinState(id, { [key]: v }), [id, key, setWinState]);
  return [value ?? fallback, setValue];
}

// useWinFocused reports whether the app's window is the focused one.
export function useWinFocused(): boolean {
  const id = useContext(WinContext);
  return useDesktop((s) => s.focused === id);
}
