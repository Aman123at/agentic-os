# Installing Chromium lazily, and Compose parity

Type: grilling
Status: open
Blocked by: 05

## Question

Settled: the release tarball does not carry Chromium. A user sets `include_browser: true` in `config.yml` and restarts; at that point the headless shell is fetched.

Today it is a build-time concern: the Dockerfile's `browser-dist` stage runs `npx playwright install --only-shell chromium`, strips locales, copies `libgbm.so.1` beside it, symlinks `/opt/aos-browser/chrome`, and `ldd`-checks the result — plus an apt install of roughly twenty shared libraries and two font families into the image.

Settle:

1. **Where the download comes from at runtime.** `npx playwright install` needs Node, which the native install deliberately does not require. Playwright's CDN can be fetched directly if the URL scheme and version pinning are pinned down — that may want a research ticket of its own.
2. **The shared libraries and fonts.** Chromium needs ~20 packages the base image installs at build time. Natively that means `apt-get install` on the user's server — a real change to their system, which is exactly what the Install Ledger and Approvals exist to govern. Does this go through them?
3. **When it happens.** During `aos service start` (a restart that silently takes minutes and may fail), or as an explicit `aos browser install` that the restart merely checks for? The latter makes progress, failure and disk cost visible.
4. **Progress and failure.** 250 MB on a slow VPS link; what the user sees, what happens on a half-download, and what happens if the disk is full.
5. **Disk check and uninstall.** Refusing when space is short; an `aos browser remove`.
6. **Compose parity.** Compose uses a build argument today, so its browser decision is made at build time. With the image published to Docker Hub prebuilt, does the published image include Chromium, ship in two variants, or fetch lazily too?
7. **Version pinning** — `PLAYWRIGHT_VERSION` is pinned in the Dockerfile and must stay in step with `desktop/package.json`. Where does that pin live once the download is at runtime?
8. **Chromium's own sandbox, natively** — see the constraint below; it is not optional for M6.

## Constraint from *Which decisions become ADRs* (resolved 2026-09-17)

**The `--no-sandbox` justification dies with the container, and nothing here notices.** ADR-0008 says Chromium's own sandbox is off "because it needs user namespaces Docker's default seccomp profile refuses. **The container is the outer boundary.**" `internal/browser/browser.go:31` repeats it in a comment, and `:36` passes the flag.

On a native install Docker's seccomp profile is not in the way, so the *reason* for the flag is gone — and the container that made it acceptable is gone in the same move. That leaves a browser rendering arbitrary web pages, unsandboxed, as a uid that sits in `NOPASSWD:ALL` sudoers. This ticket must answer it: does the native build drop `--no-sandbox` (and what does the headless shell then need — user namespaces unprivileged-enabled, or the setuid `chrome-sandbox` helper), and does Compose keep it?

Whatever this decides is **ADR-0008's amendment**, which is owed to this ticket along with the build-time-to-runtime move. It is question 8 above, and it is not optional for M6.

## Input from *Mode switching and the single binary* (resolved 2026-09-17)

**The Dockerfile's `cli` and `ui` targets collapse into one runtime image** and `AOS_IMAGE_MODE` is deleted. That leaves `ui-browser-true` / `ui-browser-false` as the **only** remaining build-time fork in the file, so collapsing it is this ticket's to finish — after which the Dockerfile has exactly one runtime stage and `INCLUDE_BROWSER` is a config key rather than a build argument.

Also relevant: `cli` Mode now starts **no TCP listener at all**, so the Browser — already `ui`-only — has no surface to reach in `cli` Mode either way.
