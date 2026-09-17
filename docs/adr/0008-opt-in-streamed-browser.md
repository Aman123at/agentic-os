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

## M6 amendment (native install, 2026-09-17)

Two claims in the Consequences above are container-specific. "The container is the
outer boundary" for `--no-sandbox` **vanishes natively**: there is no container, so
the browser gets **its own narrow Landlock ruleset** (its profile and `~/Downloads`
only) rather than borrowing the Agent's, or M6's widening to `/` would hand an
unsandboxed Chromium write access to the whole server (M6.7). `--no-sandbox` is
kept either way, and whether Chromium's own sandbox *could* work at all depends on
the Ubuntu version (22.04 allows unprivileged user namespaces; 24.04+ refuses them
via AppArmor) — the ADR states the dependence rather than a flat impossibility;
the answer is a question for Aman.

The image no longer bakes the headless shell in. `sudo aos browser install` fetches
Chrome-for-Testing on demand — a 120 MB zip keyed by the Chrome version, pinned by
**a sha256 we pin ourselves** (Playwright verifies nothing), checked with a
`debug/elf` `DT_NEEDED` scan against `ldconfig -p`, refused under 1 GB free, and
kept **out of the Install Ledger** (Restore would otherwise remove its libraries
out from under it). This gives true Compose/native parity and leaves the Dockerfile
with one runtime stage. The setuid `chrome_sandbox` path is dead twice over: it is
not in the headless-shell zip, and `no_new_privs` makes it inert. See M6.11.
