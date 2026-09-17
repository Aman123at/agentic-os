# Installing Chromium lazily, and Compose parity

Type: grilling
Status: resolved
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

## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

### Four findings that reshaped the ticket

**Playwright is not in the download path. Google is.** Checked live rather than assumed:
`GET https://cdn.playwright.dev/builds/cft/153.0.8010.12/linux64/chrome-headless-shell-linux64.zip`
answers **307** to `https://storage.googleapis.com/chrome-for-testing-public/153.0.8010.12/linux64/chrome-headless-shell-linux64.zip`, which is **200, 119,809,080 bytes**. `playwright-core`'s registry (`desktop/node_modules/playwright-core/lib/coreBundle.js:32652`, `cftUrl`) builds that path from the **Chrome version string** (`153.0.8010.12`), not from the Playwright revision (`1243`) — the revision is used only for Firefox and WebKit, which are Playwright's own builds. So question 1 holds no research ticket: the native fetch is one GET of a zip from a Google bucket, and the pin is a Chrome version, not `PLAYWRIGHT_VERSION`. Also established: **Playwright verifies no checksum** — `createHash` appears in its bundle only for WebAuthn, and the download path trusts TLS and nothing else.

**The setuid sandbox helper is unavailable twice over.** The zip's central directory, read by range request, holds 287 entries and **no `chrome_sandbox`**; that binary ships only in the full `chrome-linux64` build. And `internal/sandbox/landlock_linux.go:55` sets `PR_SET_NO_NEW_PRIVS` on every thread before exec, deliberately, so a setuid helper would be inert even if we shipped one. The ticket's own question 8 option (b) is dead on arrival, and the ADR has to say so or it gets re-proposed.

**`--no-sandbox` loses its reason and gains a different one, and the sudoers alarm was overstated.** ADR-0008 blames Docker's seccomp profile; natively that is gone. But Ubuntu 24.04+ refuses unprivileged user namespaces itself (`kernel.apparmor_restrict_unprivileged_userns=1`) for any binary without an AppArmor profile granting `userns create`, so on the target OS the flag is still needed — for a new reason, removable only by lowering a kernel hardening default or installing a profile. Meanwhile *Which decisions become ADRs* recorded the risk as an unsandboxed browser running as a uid in `NOPASSWD:ALL` sudoers: that part is **wrong**. The browser is launched through `sandbox.Command`, so `no_new_privs` makes sudo inert for it and everything it spawns. The exposure is filesystem, not root.

**The browser inherits the *Agent* ruleset, so widening the filesystem widens the browser.** `internal/daemon/browser_linux.go:69` is `d.plan(d.agentPolicy())`. Today that means home, `/tmp`, `/var/tmp` and `/dev` writable and the rest read-only. After *Widening the filesystem to the whole VPS*, `Writable` becomes `/` minus a Protected list — so an unsandboxed Chromium rendering arbitrary pages, which Agents can point anywhere, would acquire write access to the whole server as a side-effect of a decision taken in another ticket about Agents. Nobody had booked it.

### Decisions

1. **The download is Chrome for Testing, fetched directly.** `https://storage.googleapis.com/chrome-for-testing-public/<version>/linux64/chrome-headless-shell-linux64.zip` (and `linux-arm64`), with `cdn.playwright.dev/builds/cft/…` as the one documented fallback mirror — which is what Playwright's own mirror list does, and it is free. No Node, no `npx`, no Playwright at runtime. **The pin is the Chrome version plus a sha256 per architecture, in Go source beside `internal/browser`**, not in `config.yml`: it is a property of the release, not of the user's configuration. Because Playwright checks nothing, pinning our own hash is a strict improvement on what the image does today. The Dockerfile uses the same pin, so the image and the native install run identical bytes.

2. **CI keeps the pin in step with the Desktop.** A check comparing our pinned Chrome version against `playwright-core/browsers.json`'s `chromium-headless-shell.browserVersion`, failing with the line to change. That is the real content of question 7: `PLAYWRIGHT_VERSION` leaves the Dockerfile, and what must not drift is a version string CI can read out of the lockfile tree.

3. **The libraries are the curated list, installed as ordinary packages.** The zip ships its own `deb.deps` (fetched: 30 entries, including `libgtk-3-0`, `libcups2`, `libpango-1.0-0`, `wget`, `xdg-utils`) — that is *Chrome's* packaging dependency list, not the headless shell's, and following it would drag GTK, CUPS and Mesa onto the user's server for nothing. The Dockerfile's hand-curated ~20 packages move into one list shared by the image and the installer. **`libgbm1` becomes a plain package**, deleting the `apt-get download` + `dpkg-deb -x` extraction hack and `browser.LibDir` with it: that trick existed to keep Mesa and LLVM out of an *image*, and after decision 9 the browser is not in the image at all, so there is no image size left to protect. Cost, stated plainly: about 180 MB of Mesa and LLVM on the user's disk, which is why decision 8's figure is what it is.

4. **The verification is `debug/elf`, not `ldd`, and it runs at install time.** For each `DT_NEEDED` entry in the downloaded binary, check the name appears in `ldconfig -p`. Stdlib only, consistent with ADR-0002; it never executes a binary that arrived over the network; and it works identically in the image and natively. This is a **refinement on the grilling**, which said "the `ldd` check moves into the verb" — the move is the decision, `debug/elf` is how. The check that was protecting the build now protects the user, who never had one.

5. **The apt install does not go through the Install Ledger.** Not a preference — a hazard. Restore undoes what came after a Checkpoint, so a Checkpoint taken before the browser install and restored months later would `apt-get remove` those libraries out from under a browser that `config.yml` still says is enabled, leaving a Browser app that fails to start with nothing to explain it. The Ledger records what **Agents** change with root authority so the user can undo it; this is the administrator, at the console, with sudo, changing the product's own installation. It belongs where `aos daemon` and `aos uninstall` are recorded, not where `install_package` is.

6. **The verb is `sudo aos browser install`, and it never happens during a start.** On success it **writes `include_browser: true` itself**, so one command does the whole thing and the key becomes the record rather than the trigger. A start with `include_browser: true` and nothing at `/opt/aos-browser` takes the existing path at `internal/daemon/browser_linux.go:23`, with the message changed from "rebuild the image" to `sudo aos browser install`. `aos browser remove` deletes the tree and sets the key back to false; it does **not** remove the apt packages, by ticket 11's rule that uninstalling AOS is not a reason to uninstall nginx. `aos browser` with no verb says whether it is installed and at what version.

7. **It stays at `/opt/aos-browser`.** Not under `/var/lib/aos`, which `internal/sandbox/policy.go:54` marks `Hidden` — a Landlock-confined browser could not execute itself from there.

8. **Progress, failure, disk.** Download to `/opt/aos-browser.tmp` and `rename()` into place only after decision 4's check passes, so a half-download is never mistaken for an install and the repair is re-running the command — ticket 11's "re-running is the repair" rule. Progress as bytes, total and percent on one rewriting line; plain lines when stdout is not a tty. **No resume**: Range requests on a 120 MB file buy a minute and cost a partial-file trust problem. **Refuse before the first byte when the filesystem holding `/opt` has under 1 GB free**, naming both the requirement and what is actually free: ~120 MB zip, ~250 MB unpacked (the shell alone is 197 MB), plus decision 3's packages, plus headroom. `internal/sysinfo/metrics.go:123` already wraps `unix.Statfs`, so this is reuse. Locales are stripped to `en-US` after unpacking, as the image does now — about 45 MB. Unpacking is `archive/zip`, preserving the stored unix mode (`chrome-headless-shell` is `0755` in the archive). `aos uninstall --purge` removes `/opt/aos-browser`; without `--purge` it survives, like `/var/lib/aos`.

9. **Compose parity: the published image carries no Chromium and no browser libraries at all.** `aos browser install` does the same work inside the container. That is true parity — one code path, one documentation section, one Docker Hub tag at ~150 MB compressed — and it finishes what *Mode switching* handed over: `INCLUDE_BROWSER` dies as a build argument, the `ui-browser-true` / `ui-browser-false` stages die, the `browser-dist` stage dies, and the Dockerfile is left with **exactly one runtime stage**. Two costs, stated: `compose.yaml` needs a named volume for `/opt/aos-browser` or a recreated container loses the browser; and `tools/ci`'s `ui+browser` image disappears, replaced by a run of `aos browser install` inside a container — which downloads 120 MB, so it is a named stage run on demand, not on every PR. The check at `tools/ci:373` flips from "only the `INCLUDE_BROWSER` image has it" to "no image has it".

10. **Chromium's own sandbox: keep `--no-sandbox`, rewrite the reason, and pay for it where it is payable.** `startBrowser` stops calling `d.agentPolicy()` and gets a **`browserPolicy()` that is an allow-list**: writable — the profile directory and `~/Downloads`; readable — `/opt/aos-browser` and the font and library paths it needs; everything else absent by omission. Finding 4 is then closed before it opens, and `--no-sandbox` costs a compromised renderer the browser's own profile rather than the server. **Ordering**: this must land **before or with** the filesystem widening, for the same reason the forwarder fix must land with the bind change — in between, there is a commit where an unsandboxed browser can write to `/`. Considered and rejected, all three named in the ADR: relaxing `kernel.apparmor_restrict_unprivileged_userns` (an installer lowering a kernel hardening default so a browser sandbox can start is a worse trade than a ruleset we already own), shipping an AppArmor profile (the same, with more moving parts), and switching to the full Chrome build for its `chrome_sandbox` (finding 2 — `no_new_privs` makes it inert). ADR-0008's consequence bullet becomes: the browser's own Landlock ruleset is the boundary, and it is narrower than an Agent's.

11. **A separate uid for the browser is not in M6**, named rather than silently skipped. With `no_new_privs` and decision 10's ruleset, a second uid mainly buys separation from the Agent's home, and it costs `~/Downloads` and the profile, which live in `aos`'s home and are read by Finder. Worth revisiting if the Browser ever gains a persistent logged-in profile.

12. **One fact this Mac cannot supply: which Ubuntu the VPS runs.** On 22.04 unprivileged user namespaces are allowed by default and dropping `--no-sandbox` would work; on 24.04+ it would not. Decision 10 keeps the flag either way, because the product has to run on both — but it changes what the ADR may claim is *possible*, so the ADR states the version dependence rather than asserting a flat impossibility. Chrome's own support matrix, read from `coreBundle.js`, is what `aos browser install` refuses against: **ubuntu22.04, ubuntu24.04, ubuntu26.04, debian12 and debian13 are supported; ubuntu18.04 and ubuntu20.04 are not.**

### Surfaces

`internal/browser/browser.go` (`Binary` keeps its path, `LibDir` and the `LD_LIBRARY_PATH` that uses it are deleted, the `--no-sandbox` comment is rewritten, the pin is added), `internal/daemon/browser_linux.go` (`browserPolicy`, the changed messages), a new `internal/browser/install.go` plus `internal/cli/browser.go`, `internal/api/ws.go:81` and `internal/api/server.go`'s `browser_unavailable` text, `internal/cli/doctor.go:49,53`, `internal/config` (`IncludeBrowser` loses its `INCLUDE_BROWSER` parsing), `docker/Dockerfile` (three stages deleted), `compose.yaml` (build arg out, volume in), `tools/ci/main.go:26,38,372`, `desktop/e2e/global-setup.ts:49` and `desktop/src/apps/registry.ts:68,168`, and `README.md:16`.

### Owed to other tickets

- ***Which decisions become ADRs*** (15): ADR-0008's amendment is decision 10 in full — the replacement boundary, the three rejections with their reasons, and the version-dependent truth about unprivileged user namespaces. Two corrections to that ticket's own findings: the sudoers framing is wrong (`no_new_privs`), and the setuid helper is doubly unavailable. ADR-0003 gains a sentence from decision 5 on what the Ledger is *not* for.
- ***Write M6 into docs/PLAN.md*** (16): five sub-tasks, and one of them is order-critical — see the input note appended there.
- ***The documentation site*** (13): the Browser page stops describing a build flag and describes a command, with the disk cost and the supported-distribution list.
- ***/etc/aos/config.yml*** (05): `include_browser` stays startup-only, but it is now written by `aos browser install` as well as by hand, and its documented meaning is "the browser is installed and on" rather than "fetch the browser".
- ***Release engineering*** (09): `tools/ci` loses the `ui+browser` image and gains two stages — the pin-drift check (cheap, every run) and the browser install rehearsal (120 MB, on demand).
