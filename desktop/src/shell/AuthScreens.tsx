// The Desktop's authentication screens (ADR-0007, PLAN.md §18 M6.5): the sign-in
// form (a full-screen boot phase, and the same form inside the expiry modal), and
// the forced first-change form. Both are plain controlled forms over aosd's auth
// RPCs; the store holds the phase and the message they show.
import { useState, type FormEvent } from "react";

import { MIN_PASSWORD_LENGTH, useDesktop } from "../store";

// AuthCard is the boot-card shell the screens share, so a login and the boot
// splash look like one product. In the Root Realm it names the Realm, so the
// person signing in knows which history and privilege they are entering (M7.10).
function AuthCard({ title, children }: { title: string; children: React.ReactNode }) {
  const rootRealm = useDesktop((s) => s.rootRealm);
  return (
    <main className="boot">
      <div className="boot__card auth__card">
        <h1 className="boot__logo">Agentic OS</h1>
        {rootRealm && <p className="auth__realm">Root Mode</p>}
        <p className="auth__title">{title}</p>
        {children}
      </div>
    </main>
  );
}

// SignInForm collects a username and password. It is used full-screen on the
// needs-signin phase and, unchanged, inside the expiry modal.
export function SignInForm({
  onSubmit,
  error,
  card = true,
  submitLabel = "Sign in",
}: {
  onSubmit: (username: string, password: string) => void | Promise<void>;
  error: string;
  card?: boolean;
  submitLabel?: string;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || !username || !password) return;
    setBusy(true);
    try {
      await onSubmit(username, password);
    } finally {
      setBusy(false);
    }
  }

  const form = (
    <form className="auth__form" onSubmit={submit}>
      <label className="auth__label">
        Username
        <input className="auth__input" name="username" autoComplete="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
      </label>
      <label className="auth__label">
        Password
        <input className="auth__input" name="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      {error && <p className="auth__error" role="alert">{error}</p>}
      <button className="auth__submit" type="submit" disabled={busy || !username || !password}>
        {busy ? "Signing in…" : submitLabel}
      </button>
    </form>
  );
  return card ? <AuthCard title="Sign in to the Desktop">{form}</AuthCard> : form;
}

// ChangePasswordForm is the forced first change (and the shape a later change
// would take): a new password, confirmed, refused below the minimum rather than
// merely warned (PLAN.md §18 M6.5).
export function ChangePasswordForm({
  onSubmit,
  error,
}: {
  onSubmit: (newPassword: string) => void | Promise<void>;
  error: string;
}) {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const tooShort = password.length > 0 && password.length < MIN_PASSWORD_LENGTH;
  const mismatch = confirm.length > 0 && confirm !== password;
  const ready = password.length >= MIN_PASSWORD_LENGTH && confirm === password;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || !ready) return;
    setBusy(true);
    try {
      await onSubmit(password);
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthCard title="Choose a password">
      <p className="boot__muted auth__hint">You signed in with a one-time password. Set your own to continue.</p>
      <form className="auth__form" onSubmit={submit}>
        <label className="auth__label">
          New password
          <input className="auth__input" type="password" autoComplete="new-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        <label className="auth__label">
          Confirm password
          <input className="auth__input" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </label>
        {tooShort && <p className="auth__error">At least {MIN_PASSWORD_LENGTH} characters.</p>}
        {mismatch && <p className="auth__error">The passwords do not match.</p>}
        {error && <p className="auth__error" role="alert">{error}</p>}
        <button className="auth__submit" type="submit" disabled={busy || !ready}>
          {busy ? "Saving…" : "Set password"}
        </button>
      </form>
    </AuthCard>
  );
}
