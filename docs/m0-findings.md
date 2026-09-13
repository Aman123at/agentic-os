# M0 findings

**Date:** 2026-09-14 · **Host:** macOS (Apple Silicon), Docker Desktop 29.7.2, kernel 7.0.12-linuxkit, Landlock ABI 8
**Scope:** PLAN.md §18, prototypes 0.1–0.8. Vocabulary follows [CONTEXT.md](../CONTEXT.md).

## Summary

| # | Prototype | Result on the macOS Host |
|---|---|---|
| 0.1 | Landlock ruleset | ✅ Protected files cannot be written, truncated, deleted, renamed or hard-linked by shell commands or Python; `/run/secrets` and `/var/lib/aos` cannot be read; `sudo` fails; a User Session's environment cannot be read and it cannot be signalled; User Sessions are unaffected. ⚠️ **F1** and **F2** need a decision. |
| 0.2 | Session framing | ✅ 1,000 mixed commands (23 kinds: binary output, NUL bytes, no trailing newline, heredocs with tabs, 100 KB command text, fake markers, background jobs, interactive prompts, `return`, redirected stdout) all framed with correct output and exit codes. Round trip p50 0.32 ms, **p95 0.46 ms** (target < 10 ms). |
| 0.3 | Replay from cache | ✅ 10 packages (17 with dependencies) reinstalled with `--network none` in a fresh container at exact versions, by two strategies (**F9**). |
| 0.4 | Compose secret | ✅ Arrives only as a file (`root:root 0444`, copied in, not bind-mounted); the key is absent from `docker inspect`, `docker compose config`, and every `/proc/*/environ` and `cmdline`. ⚠️ **F4**: `up` fails when the variable is entirely unset. |
| 0.5 | Forwarding | ✅ Subdomain and path modes (HTTP and WebSocket) work through aosd. Chrome and Safari: subdomain mode works in both (**F5**); path mode gives `origin=null`, with cookies and localStorage blocked, in both. Override file publishes a port range. Edge and Firefox are not installed on this Host. |
| 0.6 | Build | ✅ `docker compose up --build` from zsh for `cli` and `ui`; the `cli` build never runs the Node stage; `AOS_MODE=ui` on a `cli` image fails clearly; start to healthy in 0.32 s; aosd idle RSS 9.8 MB. ⚠️ **F3** size targets missed; ⚠️ **F6** Docker Desktop hangs on bind mounts from `~/Desktop`. |
| 0.7 | Low ports | ✅ `ip_unprivileged_port_start=0`; both User and Agent Sessions bind port 80. |
| 0.8 | Host check | ✅ `docker compose exec aos aos doctor --host-check` runs 0.1, 0.2, 0.4, 0.5 (without browsers) and 0.7: **54 passed, 0 failed, 2 known** (F1, F2). |

## Decisions needed before M1

### F1 — Carve-outs make new entries in split folders unwritable (changes ADR-0004, PLAN §7.2)

Landlock can only grant whole trees. To exclude `~/.ssh`, `~/.bashrc`, … the home folder is *split*: every existing entry gets full access, and home itself gets only *create*, because any stronger right on home would also apply inside `~/.ssh`. Consequence, measured: a new file or folder directly in home **can be created but not written in the same command**. `git clone … ~/repo`, `curl -o ~/file.zip`, `python3 -m venv ~/venv` and `mkdir ~/x && cd ~/x && …` all fail with "Permission denied" (and may leave empty files). Re-planning the ruleset before the next command fixes it only for later commands. Per-command rulesets cannot help: the persistent PTY bash performs redirections itself, so it must carry the narrow ruleset, and Landlock never widens.

**Option B (recommended), prototyped 20/20** (`tools/spikes/landlock-home/option-b.sh`):

- Protected dotfiles live in `/home/.aos-protected/` (owned by `aos`, outside home) and home holds **root-owned symlinks** to them: `.ssh`, `.gnupg`, `.config`, `.bashrc`, `.profile`, `.bash_logout`, plus `.bash_profile`, `.bash_login`, `.inputrc` so they cannot be planted.
- Home is `root:aos` mode `1775` (sticky): nobody but root can delete, rename or replace the symlinks.
- Agents get **full write on home**. Writes through a symlink resolve outside home and are refused by Landlock; replacing a symlink is refused by the sticky bit.
- Verified: git init and commit, create/move/delete, `mkdir -p` and `rm -rf`, and a Python venv in brand-new top-level folders all work in one command. Appending to `~/.bashrc`, `rm`/`mv`/`ln -sf` over it, writing `~/.ssh/config` or deleting keys, and touching the Shared Folder are all refused. The user still edits dotfiles, runs `git config --global`, and uses sudo.
- Trade-offs:
  - `sed -i ~/.bashrc` fails for the user, because it replaces the symlink; `sed -i --follow-symlinks` works. The user cannot delete a protected symlink; unprotecting happens through System Settings or `aos unprotect`.
  - Paths the user locks deeper inside home (🔒, `aos protect`) still use carve-outs, so F1's limitation remains inside the folder that contains the locked path. This is documented, and the Stale check re-plans before the next command.
- Home layout is created by the image and by aosd at startup (also for existing volumes).

**Option A:** keep carve-outs, re-plan between commands, and tell Agents to create things inside existing folders (`~/Projects`, `~/Downloads`). Cheaper, but common one-liners break.

### F2 — Create rights reach into the Shared Folder (changes PLAN §6.2, §7.2)

The Shared Folder is mounted at `~/Shared`, beneath home. Landlock walks up across mount points, so home's *create* right lets Agents create empty files and folders on the Host. **Fix (both options):** mount it at `/shared`, with `~/Shared` a root-owned symlink. With Option B this is required, since home becomes fully writable.

### F3 — Image size targets are missed (changes PLAN §6.3/§16)

| | unpacked | compressed | target |
|---|---|---|---|
| `cli` arm64 | 498 MB | 151 MB | < 450 MB |
| `ui` arm64 (placeholder Desktop) | 498 MB | 151 MB | < 500 MB |
| `cli` / `ui` amd64 | 476 MB | 155 MB | same |

Largest contributors: Node.js 24 (binary 118 MB + npm 19 MB), rclone 60 MB, git with Perl ≈ 72 MB, Python ≈ 70 MB, Ubuntu base ≈ 106 MB. Stripping the Node binary saves only 3 MB. Docker Desktop's image store also reports three different "sizes" (682 MB in `docker image ls`, 151 MB in `inspect`, 498 MB unpacked), so the target must name its measurement.

**Recommended:** keep the baseline as agreed. Define targets as unpacked `du` size, **`cli` < 520 MB, `ui` < 540 MB**, which leaves room for the real Desktop bundle, plus compressed < 180 MB. **Alternative:** install rclone and/or Node on demand, bringing it to ≈ 300–440 MB. `go run ./tools/ci` reports the miss without failing until this is decided.

### F4 — `OPENAI_API_KEY` must be defined, even if empty (README, PLAN §6.2)

With `environment: OPENAI_API_KEY` as the secret source, `docker compose up` **refuses to start** when the variable is unset ("required by secret … is not set"). Set to an empty string, the Machine starts and no secret file exists (`aos doctor`: "not provided"). **Action:** README step 1 is `cp .env.example .env`, and `.env.example` keeps `OPENAI_API_KEY=`. aosd treats a missing or empty file as "no key yet". The file is world-readable (0444), so Landlock is what keeps Agents out; copying it to `/var/lib/aos/keys/openai` (0400) at startup, as planned, remains necessary.

### F6 — Docker Desktop hangs on bind mounts from `~/Desktop` (README troubleshooting; your action)

Twice, a container bind-mounting anything under `~/Desktop` (this repo's `./shared`, or `bin/`) stayed in "Created" and wedged the daemon: no container could start until Docker Desktop was restarted. Mounts from `/private/tmp` work. `~/Desktop`, `~/Documents` and `~/Downloads` are protected by macOS privacy controls. **Because this repo is in `~/Desktop`, the default `./shared` mount will hang `docker compose up` on this Mac.** Fix, on your side:

- System Settings → Privacy & Security → **Files & Folders → Docker → Desktop Folder** (or Full Disk Access), then restart Docker Desktop; or
- move the repository out of `~/Desktop`; or
- set `AOS_SHARED_DIR` to a path outside those folders.

I verified every compose scenario with `AOS_SHARED_DIR` in a scratch directory, and restarted Docker Desktop twice while diagnosing this.

## Findings recorded in ADR-0004 or for M1

- **F5 — Safari resolves `*.localhost`.** This contradicts the planning note in PLAN §12/§15. Subdomain forwarding works in Safari too, so path mode is a general fallback (e.g. for proxies or other resolvers) rather than a Safari switch. Edge (Chromium) and Firefox (resolves `*.localhost` natively since v84) were not tested here.
- **F7 — Landlock does not cover metadata or unprotected dotfiles.** Agents can `chmod`/`touch` files they own, including Protected ones (contents stay intact), and can write `~/.local/bin`, where PATH shadowing is possible. Mitigations in place: Session `PATH` lists system directories before `~/.local/bin`. Option B pre-creates root-owned symlinks for login-shell dotfiles. The `chmod` gap is accepted and documented.
- **F8 — Residual risk from the shared uid.** A file an Agent writes that the user later *executes* (git hooks, Makefiles, npm scripts, venv activators) runs unconfined with sudo in a User Session. This is inherent to ADR-0004. Later mitigations: flag writes to `.git/hooks` as Risky Actions, and show a Terminal hint when running freshly Agent-written scripts.
- **F9 — Replay strategy.**
  - Both strategies work offline in a fresh container. Strategy 1, exact versions through the package lists kept in the volume, took 1.6 s. Strategy 2, installing the cached `.deb` files directly, took 0.6 s and needs no lists, so it survives a later `apt-get update` that drops old versions.
  - **Recommended:** the Install Ledger records every package, dependencies included, as `name:arch=version`. Replay installs the cached `.deb` files, re-marks auto-installed packages, and falls back to lists or network as in §11.
  - Keep the lists (54 MB) in `aos-pkgcache`. Cache clean-up must keep every `.deb` the Ledger references. Add `apt-utils` to the baseline, which silences a debconf warning.
- **F10 — Session framing design.**
  - aosd writes each command to a root-owned file under `/run/aos` and types one short line: ` __aos_c <nonce>; source <file>; __aos_d <nonce> $?`. Markers are written to `/dev/tty`, so they survive redirected stdout.
  - `exit` ends the shell, and aosd must restart the Session. Output uses CR LF (PTY), which Tools normalise.
- **F11 — Sandbox details.**
  - Landlock rejects directory rights on file rules, so the applier masks them.
  - Symlinks are never granted: Landlock would grant their target.
  - The sandbox helper receives an explicit environment, never aosd's, so `AOS_ACCESS_TOKEN` cannot leak.
  - A check counts as "denied" only if the confined shell actually started.
- **F12 — Smaller items for M1.**
  - The Host allow-list rejects LAN host names, so `AOS_BIND` beyond localhost needs configured names.
  - The proxy strips the Desktop session cookie from requests and `Set-Cookie` from responses in both modes; path mode adds `Content-Security-Policy: sandbox` without `allow-same-origin`.
  - aosd's own port is never forwarded, which would loop.
  - `sudo` is added to the baseline; it was missing from §6.3 but User Sessions need it.

## Host check for Windows and Linux Hosts

```bash
cp .env.example .env            # OPENAI_API_KEY may stay empty for the check
AOS_MODE=cli docker compose up -d --build
docker compose exec aos aos doctor --host-check
```

Please send the full report. Expected on a healthy Host: `0 failed` and 2 known findings (F1, F2). On Linux Hosts without Landlock, 0.1 reports "Landlock is available: FAIL" plus a SKIP, and the rest should pass.

## Proposed plan and ADR changes (for approval)

1. ADR-0004 and PLAN §7.2–7.3: adopt Option B (F1). Mount the Shared Folder at `/shared` (F2).
2. PLAN §6.2: volume `${AOS_SHARED_DIR}:/shared`; README quick start begins with `cp .env.example .env` (F4); troubleshooting entry for F6.
3. PLAN §6.3/§16: size targets per F3; add `sudo` and `apt-utils` to the baseline.
4. PLAN §11: Ledger records dependencies; Replay installs cached `.deb` files first (F9).
5. PLAN §12/§15: Safari supports subdomain forwarding; path mode is a general fallback (F5).
