// The sign-in lifecycle (ADR-0007, M6.5): exchanging a username and password for
// a token pair, rotating it, changing the password, signing out, and minting the
// single-use tickets that authenticate browser loads which cannot send a header
// (WebSockets, <img>/<video>, PDF ranges, downloads, a forwarded Service).
import { auth as authClient } from "./client";
import * as session from "./session";

export interface SignInResult {
  mustChange: boolean;
}

// signIn exchanges credentials for a token pair. mustChange is true when the
// account is still on its system-generated password, so the Desktop forces a
// change before the shell loads.
export async function signIn(username: string, password: string): Promise<SignInResult> {
  const r = await authClient.signIn({ username, password });
  session.setTokens(r.accessToken, r.refreshToken);
  return { mustChange: r.mustChangePassword };
}

// resume rotates the stored refresh token on boot, so a returning tab is signed
// in without asking for the password again. It throws when there is no session
// or the token no longer works; the caller then shows the login screen.
export async function resume(): Promise<SignInResult> {
  const token = session.getRefreshToken();
  if (!token) throw new Error("no session");
  const r = await authClient.refresh({ refreshToken: token });
  session.setTokens(r.accessToken, r.refreshToken);
  return { mustChange: r.mustChangePassword };
}

// changePassword replaces the password from a signed-in session (the forced
// first change, or a later one). aosd signs every other session out and returns
// this session a fresh pair, so the change does not log the user out here.
export async function changePassword(newPassword: string): Promise<void> {
  const r = await authClient.changePassword({ newPassword });
  session.setTokens(r.accessToken, r.refreshToken);
}

// signOut ends this session's family server-side and forgets the tokens locally.
// The store separately closes the streams and WebSockets the session authorised.
export async function signOut(): Promise<void> {
  const token = session.getRefreshToken();
  session.clearTokens();
  if (token) await authClient.signOut({ refreshToken: token }).catch(() => {});
}

// ticket mints a single-use, 30-second credential for one browser load. It rides
// an authenticated RPC, so it needs a live access token.
export async function ticket(): Promise<string> {
  const r = await authClient.createTicket({});
  return r.ticket;
}

// The proactive refresh interval. The access token lives 15 minutes server-side
// (auth.AccessTTL); refreshing every 12 keeps a live pair well ahead of expiry,
// and does not wait for a 401 that would queue behind the held event stream and
// WebSockets already occupying the browser's six connections (M6.5).
const REFRESH_INTERVAL_MS = 12 * 60 * 1000;

let timer: ReturnType<typeof setInterval> | undefined;

// startProactiveRefresh keeps the token pair fresh on a timer until stopped. A
// refresh that fails has lost the session, so it raises the expiry modal.
export function startProactiveRefresh(): void {
  stopProactiveRefresh();
  timer = setInterval(() => {
    void resume().catch(() => session.sessionExpired());
  }, REFRESH_INTERVAL_MS);
}

export function stopProactiveRefresh(): void {
  if (timer !== undefined) clearInterval(timer);
  timer = undefined;
}
