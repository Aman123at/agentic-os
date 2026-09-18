// Typed Connect-RPC clients over aosd's HTTP API (PLAN.md §13). The Desktop is
// served from aosd's own origin. Every request carries the in-memory access
// token in an Authorization header (ADR-0007, M6.5) — never a cookie, so DNS
// rebinding gains nothing. A 401 despite the proactive refresh means the session
// is truly dead, so the transport raises the expiry modal.
import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import {
  ApprovalService,
  AuthService,
  EventService,
  FileService,
  SessionService,
  SettingsService,
  SoftwareService,
  SupervisorService,
  SystemService,
  TaskService,
  TrashService,
} from "../gen/aos/v1/services_pb";
import { getAccessToken, sessionExpired } from "./session";

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  fetch: async (input, init) => {
    const headers = new Headers(init?.headers);
    const token = getAccessToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const resp = await fetch(input, { ...init, headers });
    // Refresh runs proactively on a timer, so a 401 here is a session that could
    // not be kept alive: drop it and let the store show the modal. Sign-in and
    // refresh are open, so a 401 on them is a bad password, not an expiry.
    if (resp.status === 401 && token) sessionExpired();
    return resp;
  },
});

export function client<T extends Parameters<typeof createClient>[0]>(service: T): Client<T> {
  return createClient(service, transport);
}

export const auth = client(AuthService);
export const system = client(SystemService);
export const settings = client(SettingsService);
export const events = client(EventService);
export const files = client(FileService);
export const trash = client(TrashService);
export const tasks = client(TaskService);
export const sessions = client(SessionService);
export const approvals = client(ApprovalService);
export const software = client(SoftwareService);
export const supervisor = client(SupervisorService);
