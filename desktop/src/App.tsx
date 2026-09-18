import { useEffect } from "react";

import Shell from "./shell/Shell";
import { ChangePasswordForm, SignInForm } from "./shell/AuthScreens";
import { useDesktop } from "./store";

// App boots the Desktop: resume or sign in, force a first password change when
// the account is still on its generated password, then load the shell. Until
// then (or on failure) it shows the sign-in card or a small boot card.
export default function App() {
  const { phase, error, authError, boot, signIn, submitPassword } = useDesktop();

  useEffect(() => {
    void boot();
  }, [boot]);

  if (phase === "ready") return <Shell />;
  if (phase === "needs-signin") return <SignInForm onSubmit={signIn} error={authError} />;
  if (phase === "needs-password") return <ChangePasswordForm onSubmit={submitPassword} error={authError} />;

  return (
    <main className="boot">
      <div className="boot__card">
        <h1 className="boot__logo">Agentic OS</h1>
        {phase === "loading" && <p className="boot__muted">Starting the Desktop…</p>}
        {phase === "error" && <p className="boot__error">{error}</p>}
      </div>
    </main>
  );
}
