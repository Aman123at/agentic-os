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
