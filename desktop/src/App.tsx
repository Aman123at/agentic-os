import { useEffect } from "react";

import { useDesktop } from "./store";

// App is the M3.0 foundation screen: it signs in, reads the machine Info over
// Connect-RPC, and shows it. The full shell (menu bar, Dock, window manager)
// lands in M3.1 and mounts in place of this splash.
export default function App() {
  const { phase, error, info, boot } = useDesktop();

  useEffect(() => {
    void boot();
  }, [boot]);

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
        {phase === "ready" && info && (
          <dl className="boot__facts">
            <dt>Mode</dt>
            <dd>{info.mode}</dd>
            <dt>Version</dt>
            <dd>{info.version || "dev"}</dd>
            <dt>Model</dt>
            <dd>{info.model}</dd>
          </dl>
        )}
        <p className="boot__note">The Desktop is being built across M3.</p>
      </div>
    </main>
  );
}
