// Typed Connect-RPC clients over aosd's HTTP API (PLAN.md §13). The Desktop is
// served from aosd's own origin, so the transport talks to the same host; the
// session cookie rides along automatically once sign-in has run.
import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import {
  AuthService,
  EventService,
  SettingsService,
  SystemService,
} from "../gen/aos/v1/services_pb";

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  // Browsers attach the HttpOnly session cookie on same-origin requests.
  fetch: (input, init) => fetch(input, { ...init, credentials: "same-origin" }),
});

export function client<T extends Parameters<typeof createClient>[0]>(service: T): Client<T> {
  return createClient(service, transport);
}

export const auth = client(AuthService);
export const system = client(SystemService);
export const settings = client(SettingsService);
export const events = client(EventService);
