// A live log panel for one Service (PLAN.md §12, M4.4): SupervisorService.
// StreamLogs sends the recent output first, then new output as it comes while
// `follow` is set. Bytes are decoded as they arrive and appended, capped so a
// noisy Service can't grow the buffer without bound; the view sticks to the
// bottom unless the user has scrolled up.
import { ConnectError } from "@connectrpc/connect";
import { useEffect, useRef, useState } from "react";

import { supervisor } from "../../api/client";
import { friendlyError } from "../../api/error";

const MAX_CHARS = 200_000;

export function LogPanel({ name, onClose }: { name: string; onClose: () => void }) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const box = useRef<HTMLPreElement>(null);
  // Whether to keep the view pinned to the newest line.
  const stick = useRef(true);

  useEffect(() => {
    const controller = new AbortController();
    const decoder = new TextDecoder();
    setText("");
    setError("");
    void (async () => {
      try {
        for await (const resp of supervisor.streamLogs({ name, follow: true, tailBytes: 0 }, { signal: controller.signal })) {
          const chunk = decoder.decode(resp.data, { stream: true });
          if (!chunk) continue;
          setText((t) => {
            const next = t + chunk;
            return next.length > MAX_CHARS ? next.slice(next.length - MAX_CHARS) : next;
          });
        }
      } catch (err) {
        if (!controller.signal.aborted && ConnectError.from(err).code !== 1 /* Canceled */) {
          setError(friendlyError(err));
        }
      }
    })();
    return () => controller.abort();
  }, [name]);

  // After each append, follow the tail unless the user scrolled up to read back.
  useEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [text]);

  return (
    <div className="log">
      <div className="log__bar">
        <span className="log__title">Logs · {name}</span>
        <button className="svc__btn" onClick={onClose}>
          Close
        </button>
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      <pre
        ref={box}
        className="log__body"
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollTop + el.clientHeight >= el.scrollHeight - 8;
        }}
      >
        {text || (error ? "" : "Waiting for output…")}
      </pre>
    </div>
  );
}
