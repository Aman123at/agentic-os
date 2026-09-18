// A fake aosd for the authentication specs (PLAN.md §18 M6.5). It intercepts the
// Connect-RPC calls the Desktop makes and answers them from memory, so the auth
// flow runs with no backend: the "provider" is these route handlers. Only what
// the sign-in, forced-change, expiry and boot paths touch is faked; the rest
// (the event stream) is left to fail, which the store already treats as offline.
import type { Page, Route } from "@playwright/test";

// The account the fake knows about. Tests flip mustChange and the password.
export interface FakeOptions {
  password: string;
  mustChange: boolean;
  // When set, the event stream returns 401 this many times before succeeding, to
  // drive the expiry modal deterministically.
  streamUnauthorizedTimes?: number;
}

// Calls records what the Desktop asked for, so a spec can assert (e.g.) that
// SignOut was sent on logout.
export interface Calls {
  signIn: number;
  refresh: number;
  changePassword: number;
  signOut: number;
  subscribe: number;
}

const rpc = (name: string) => `/aos.v1.${name}`;

// json fulfils a Connect unary reply.
function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

// unauthorized fulfils a Connect "unauthenticated" error (HTTP 401).
function unauthorized(route: Route, message = "the username or password is incorrect") {
  return route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: "unauthenticated", message }) });
}

// installFakeBackend wires the routes onto a page before it loads the app. It
// returns the mutable options (so a test can change the password mid-run) and a
// live call tally.
export async function installFakeBackend(page: Page, opts: FakeOptions): Promise<{ opts: FakeOptions; calls: Calls }> {
  const calls: Calls = { signIn: 0, refresh: 0, changePassword: 0, signOut: 0, subscribe: 0 };
  const tokens = () => ({ accessToken: "access-token", refreshToken: "refresh-token", mustChangePassword: opts.mustChange });

  await page.route(`**/aos.v1.**`, async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = () => {
      try {
        return JSON.parse(route.request().postData() ?? "{}") as Record<string, unknown>;
      } catch {
        return {};
      }
    };
    switch (path) {
      case rpc("AuthService/SignIn"): {
        calls.signIn++;
        const { password } = body();
        if (password !== opts.password) return unauthorized(route);
        return json(route, tokens());
      }
      case rpc("AuthService/Refresh"):
        calls.refresh++;
        return json(route, tokens());
      case rpc("AuthService/ChangePassword"): {
        calls.changePassword++;
        const { newPassword } = body();
        if (typeof newPassword !== "string" || newPassword.length < 12) {
          return route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ code: "invalid_argument", message: "the password must be at least 12 characters" }) });
        }
        // A real change clears the forced flag and keeps the caller signed in.
        opts.mustChange = false;
        opts.password = newPassword;
        return json(route, { accessToken: "access-token-2", refreshToken: "refresh-token-2" });
      }
      case rpc("AuthService/SignOut"):
        calls.signOut++;
        return json(route, {});
      case rpc("EventService/Subscribe"):
        calls.subscribe++;
        if (opts.streamUnauthorizedTimes && opts.streamUnauthorizedTimes > 0) {
          opts.streamUnauthorizedTimes--;
          return unauthorized(route, "this session has expired; sign in again");
        }
        // A stream the store can't parse reads as offline; the shell still loads.
        return route.fulfill({ status: 200, contentType: "application/connect+json", body: "" });
      // The rest of what boot fetches — enough to reach the shell.
      case rpc("SystemService/Info"):
        return json(route, {});
      case rpc("TaskService/ListTasks"):
        return json(route, { tasks: [] });
      case rpc("ApprovalService/ListPending"):
        return json(route, { approvals: [] });
      case rpc("SystemService/ListNotifications"):
        return json(route, { notifications: [] });
      case rpc("SettingsService/GetDesktopState"):
        return json(route, { state: "" });
      case rpc("TrashService/ListTrash"):
        return json(route, { items: [] });
      default:
        return json(route, {});
    }
  });

  return { opts, calls };
}

// seedSession puts a refresh token in localStorage before the app loads, so boot
// takes the resume path instead of showing the login screen.
export async function seedSession(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("aos.refresh", "refresh-token");
    } catch {
      // Ignore; the test that needs a session will fail loudly instead.
    }
  });
}
