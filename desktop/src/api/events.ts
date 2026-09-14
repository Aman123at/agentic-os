// A reconnecting subscription to aosd's event stream (PLAN.md §4.3). Every state
// change aosd publishes arrives here; the store applies them so that several
// browser tabs, each with their own stream, converge on the same server state.
import { ConnectError } from "@connectrpc/connect";

import type { Event } from "../gen/aos/v1/services_pb";
import { events } from "./client";

export type ConnState = "connecting" | "online" | "offline";

interface Options {
  onEvent: (event: Event) => void;
  onState: (state: ConnState) => void;
}

// subscribe streams events until the returned function is called. It reconnects
// with backoff whenever the stream drops (aosd closes a subscriber that falls
// behind), so a tab left open recovers on its own.
export function subscribe({ onEvent, onState }: Options): () => void {
  const controller = new AbortController();
  let backoff = 500;

  async function loop() {
    while (!controller.signal.aborted) {
      onState("connecting");
      try {
        for await (const resp of events.subscribe({}, { signal: controller.signal })) {
          backoff = 500;
          onState("online");
          if (resp.event) onEvent(resp.event);
        }
      } catch (err) {
        if (controller.signal.aborted || ConnectError.from(err).code === 1 /* Canceled */) return;
      }
      if (controller.signal.aborted) return;
      onState("offline");
      await sleep(backoff, controller.signal);
      backoff = Math.min(backoff * 2, 10_000);
    }
  }
  void loop();
  return () => controller.abort();
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}
