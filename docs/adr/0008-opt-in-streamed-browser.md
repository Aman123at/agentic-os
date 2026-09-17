# An opt-in Browser: one streamed page, not a streamed desktop

The Desktop gains a Browser app when the Machine is built with `INCLUDE_BROWSER=true` (off by default, `ui` Mode only). Chromium's headless shell renders one page inside the Machine and streams it into a Desktop window as JPEG frames over the DevTools protocol (`Page.startScreencast`); the window sends mouse, wheel and keyboard input back the same way, over `GET /ws/browser`. This amends ADR-0001, which kept "a single streamed app window" open as a later addition: this is that addition, for one app, and it leaves the rest of the Desktop native.

We chose the headless shell because it is the lightest browser that renders today's web and can stream itself: there is no X server, window manager, VNC or noVNC in the image. It adds about 250 MB, so it stays out of the default build, and the Browser starts only when a window opens and stops five minutes after the last one closes.

## Consequences

- The browser runs as `aos`, confined with Landlock like an Agent, so Protected Paths stay out of its reach; Chromium's own sandbox is off (`--no-sandbox`), because it needs user namespaces Docker's default seccomp profile refuses. The container is the outer boundary.
- DevTools speaks over a pipe (`--remote-debugging-pipe`), never a port, so no Agent, Service or page in the Machine can drive the browser.
- Only `http`, `https` and `about:blank` load; aosd's own port is refused. Pages open inside the Machine, so `localhost` reaches its Services.
- Every Desktop tab sees the same page. Copying out of the page reaches the Machine's clipboard, not the Host's; pasting in works.
- User browsing isn't an Agent action and isn't in the Audit Log. Agents' use of the page is (see the addendum).

## Addendum (M5.3): Agents use the same page

Agents drive the page the user watches rather than a browser of their own: the user sees each step and can take over, and there is one browser to confine. One Task at a time holds the page through a lease. Agents read the page as text (its readable text and numbered interactive elements) instead of screenshots, which keeps the model's context small and needs no image input. Their scripts run in an isolated world, which the page can't see or change. Clicks and typing are real input events, so pages behave as they would for the user. A click that submits a form, or typing with Enter, is a Risky Action granted per site. Password, card and code fields are never filled. The DevTools calls this needs are made only by aosd's own code, never by Commands from a window.

## Considered Options

- **Firefox, or any browser behind Xvfb + VNC**: rejected; a display server and VNC stack are heavier and slower than a browser that streams itself.
- **Opera / "Opera Light"**: rejected; no Linux arm64 build and no lite desktop edition.
- **NetSurf, Dillo, surf**: tiny, but they can't run modern JavaScript sites, and they'd still need X and VNC.
- **An `<iframe>` in the Desktop**: rejected; most sites refuse to be framed, and it would browse from the Host, not the Machine.
