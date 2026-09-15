// usePoll runs an async function on mount and then every `ms` milliseconds while
// the component is mounted (PLAN.md §4.3, M4.4: Activity Monitor polls only the
// tab on screen). A run never overlaps the previous one, and the effect stops
// applying results once unmounted.
import { useEffect } from "react";

export function usePoll(fn: (signal: AbortSignal) => Promise<void>, ms: number, deps: unknown[] = []) {
  useEffect(() => {
    const controller = new AbortController();
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;

    const tick = async () => {
      try {
        await fn(controller.signal);
      } finally {
        if (!stopped) timer = setTimeout(() => void tick(), ms);
      }
    };
    void tick();

    return () => {
      stopped = true;
      controller.abort();
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
