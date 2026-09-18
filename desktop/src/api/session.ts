// The Desktop's credentials (ADR-0007, M6.5). The access token is held in memory
// so it never outlives the tab; the refresh token lives in localStorage, which is
// XSS-readable — said plainly here as in the docs — because a background tab that
// slept past the access token's 15 minutes must still recover without a new
// sign-in. Both travel in headers, never cookies, so DNS rebinding gains nothing.
//
// This module is deliberately dependency-free: the transport reads the access
// token from here, and api/auth.ts (which does the RPCs) writes it, so neither
// has to import the other.

const REFRESH_KEY = "aos.refresh";

let accessToken = "";
let expiredHandler: (() => void) | null = null;

export function getAccessToken(): string {
  return accessToken;
}

// setTokens records a fresh pair from a sign-in, refresh or password change.
export function setTokens(access: string, refresh: string): void {
  accessToken = access;
  try {
    localStorage.setItem(REFRESH_KEY, refresh);
  } catch {
    // A private window may refuse storage; the in-memory access token still
    // carries this tab until it is closed.
  }
}

export function getRefreshToken(): string {
  try {
    return localStorage.getItem(REFRESH_KEY) ?? "";
  } catch {
    return "";
  }
}

// clearTokens forgets the session locally (sign-out, or a dead session).
export function clearTokens(): void {
  accessToken = "";
  try {
    localStorage.removeItem(REFRESH_KEY);
  } catch {
    // Nothing to do; the in-memory token is already gone.
  }
}

// hasSession reports whether a refresh token is stored, so a reload can try to
// resume rather than show the login screen.
export function hasSession(): boolean {
  return getRefreshToken() !== "";
}

// onExpired registers the one handler the store uses to raise the expiry modal.
export function onExpired(fn: () => void): void {
  expiredHandler = fn;
}

// sessionExpired is called when the refresh token no longer works: the stored
// tokens are dropped and the store is told to show the modal over the desktop.
export function sessionExpired(): void {
  clearTokens();
  expiredHandler?.();
}

// authFetch is fetch with the access token attached, for the raw byte and upload
// endpoints that do not go through the RPC transport (a text peek's Range read,
// an upload POST). A load that cannot carry a header (an <img>, a WebSocket)
// uses a single-use ticket in the query instead — see api/auth.ts.
export async function authFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const headers = new Headers(init?.headers);
  if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
  const resp = await fetch(input, { ...init, headers });
  if (resp.status === 401 && accessToken) sessionExpired();
  return resp;
}
