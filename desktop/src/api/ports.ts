// A listening port opens through aosd's forwarding (PLAN.md §12), in a new tab
// from the user's click — which browsers allow where a pushed pop-up would be
// blocked. The forwarder runs inside the authenticator (M6.4): the first load
// carries a single-use ticket, which aosd exchanges for a cookie scoped to the
// Service's path, so the page's many sub-resource loads are authenticated
// without a header. The canonical /port/<n>/ form is the one that works from a
// remote browser. The Notification Center and Activity Monitor's Services & Ports
// open ports this way.
import { ticket } from "./auth";

export async function openPort(port: number): Promise<void> {
  // Open the tab synchronously on the click so the browser does not block it,
  // then send it to the ticketed URL once the ticket is minted. noopener cannot
  // be used here (it returns no handle), so the opener is severed by hand; the
  // forwarded page is sandboxed to an opaque origin anyway (M6.4).
  const tab = window.open("about:blank", "_blank");
  if (tab) tab.opener = null;
  const t = await ticket();
  const url = `${window.location.origin}/port/${port}/?ticket=${encodeURIComponent(t)}`;
  if (tab) tab.location.href = url;
  else window.open(url, "_blank", "noopener");
}
