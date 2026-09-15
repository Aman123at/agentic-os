// A listening port opens through aosd's forwarding at <port>.localhost
// (PLAN.md §12), in a new tab from the user's click — which browsers allow where
// a pushed pop-up would be blocked. The Notification Center and Activity
// Monitor's Services & Ports both open ports this way.
export function openPort(port: number) {
  const { protocol, port: hostPort } = window.location;
  window.open(`${protocol}//${port}.localhost${hostPort ? `:${hostPort}` : ""}/`, "_blank", "noopener");
}
