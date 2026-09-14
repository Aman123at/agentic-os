import { useEffect } from "react";

import Shell from "./shell/Shell";
import { useDesktop } from "./store";

// App boots the Desktop: sign in, load Info and the saved layout, then show the
// shell. Until then (or on failure) it shows a small boot card.
export default function App() {
  const { phase, error, boot } = useDesktop();

  useEffect(() => {
    void boot();
  }, [boot]);

  if (phase === "ready") return <Shell />;

  return (
    <main className="boot">
      <div className="boot__card">
        <h1 className="boot__logo">Agentic OS</h1>
        {phase === "loading" && <p className="boot__muted">Starting the Desktop…</p>}
        {phase === "needs-signin" && (
          <p className="boot__muted">
            Run <code>aos desktop-url</code> inside the Machine and open the link it prints.
          </p>
        )}
        {phase === "error" && <p className="boot__error">{error}</p>}
      </div>
    </main>
  );
}
