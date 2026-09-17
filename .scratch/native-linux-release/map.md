# Map: Native Linux release

Charted 2026-09-17 with `/wayfinder`. Tracker: local markdown (no `docs/agents/issue-tracker.md` exists; the default convention applies).

## Destination

An **M6 section in `docs/PLAN.md`, reviewed and approved by Aman**, that specifies the native Linux (Ubuntu VPS) distribution of Agentic OS — prebuilt binaries, `install.sh`, the `aos service` / `aos config` / `aos model` verbs, username+password UI authentication, whole-filesystem access, and a separate documentation site — broken into sub-tasks small enough to track one by one, plus the ADRs those decisions demand.

Reached when: M6 is written, every decision below is settled, and Aman has approved it. **No implementation code is written before that approval.**

## Notes

- **Domain**: `CONTEXT.md` is the glossary; every capitalised term (Machine, Host, Task, Agent, Tool, Session, Protected Path, Mode, Service) is defined there. This effort **collapses Machine and Host into one thing** on a VPS, which the glossary currently says are different. Fix the glossary before the vocabulary drifts.
- **Skills every session should consult**: `grilling` and `domain-modeling` by default; `research` for the research tickets; `prototype` for the prototype tickets.
- **Standing preferences (Aman)**:
  - Never start coding without explicit plan approval. Plan first, then wait.
  - Nothing is committed or pushed without asking.
  - The OpenAI key is Aman's alone — it lives in his local `.env`, never handled by the agent, and the key-spending `live` CI suite runs only when named.
  - Linux only. macOS and Windows Hosts are explicitly not a concern for this effort.
  - Testing happens on Aman's own Ubuntu VPS. This Mac cannot exercise the target environment, so ship unit and logic tests, not host-level ones.
  - Break work into very small sub-tasks so progress is trackable on both sides.
- **The repo is mid-flight**: M5.1/M5.2/M5.3 are built but uncommitted, and M4 still awaits sign-off (`docs/PLAN.md` §18). M6 stacks on top of that tree.
- **There is no git remote and no tags.** `github.com/Aman123at/agent-os` does not exist as a remote here yet; the release workflow assumes it will.

## Decisions so far

### Settled while charting (2026-09-17, three grilling rounds — no ticket)

- **Destination artifact**: an M6 section in `docs/PLAN.md` plus ADRs, not a standalone spec and not the feature itself.
- **Deployment shape**: a **native host install** becomes the primary shape — `aosd` installed to `/usr/local/bin`, run by systemd on the VPS itself, Machine == VPS. Docker Compose stays as the sandboxed alternative, and the docs state plainly that only the native install reaches the real host filesystem.
- **CLI verbs**: `aos service start|stop|status|restart` controls the service. `aos stop --all` keeps its current meaning (stop every Agent) untouched.
- **Distribution**: prebuilt release tarballs only. No Go, Node or any toolchain required on the VPS — installing is a software install, not a build.
- **Installation is non-interactive**: no prompts, no key, no questions. Install, then configure.
- **Configuration lives in `/etc/aos/config.yml`**, root-owned `0600` — deliberately *not* under `/home/aos`, which is the Agents' writable tree. `aos config set` and the UI override at runtime; a restart re-reads the file.
- **Config precedence**: runtime changes are written back into `config.yml`, so the file stays the single source of truth and a restart never silently reverts a setting (Q19 option (b)).
- **Port**: base **7700**, scanning upward if taken; the chosen port is recorded and printed by `aos service status`; an explicit `port:` pins it and fails loudly rather than moving.
- **Bind**: `0.0.0.0` (settled 2026-09-17, reversing the earlier `127.0.0.1`). A fresh install is reachable from a browser at `http://IP:7700` with no proxy in front; nginx and TLS stay optional and are the user's own business. The password screen, not the bind address, is what stands between the internet and the Machine — which raises the stakes on *The authentication model* and on the two `Host` checks, since both refuse anything that is not localhost today.
- **Authentication**: username + password, stored in the existing SQLite database, one user only. JWT access token plus a working refresh token, signup on first UI use, password change requiring the current password, logout. No email or SMS, so no forgotten-password flow. The CLI is not authenticated — it is never exposed.
- **Agent identity**: Agents run as the unprivileged `aos` user, which is in sudoers; privileged actions go through the existing Privileged Tool + Approval path, so root is reachable but always explicit and audited.
- **Agent reach**: Agents read the whole filesystem and may write outside `/home/aos`, with the existing Protected Paths asking first. Other users' homes join the built-in Protected list.
- **AOS owns its own home**: `/home/aos`, created by the installer. `PrepareHome`'s dotfile relocation must **never** run against a home AOS did not create — pointed at a live `/home/ubuntu` it would move the `authorized_keys` that is the only way into the server.
- **Replay is off in native mode**: the Install Ledger is still recorded and Checkpoint/Restore stay available but user-initiated. Re-applying the Ledger at every boot makes sense for a disposable container and is destructive on a persistent server.
- **Shared Folder is removed**, everywhere including Compose.
- **Finder's Places** become Home, Downloads, Filesystem (`/`) and Trash.
- **Mode** is a runtime setting in `config.yml`; one binary always carries the embedded Desktop (3.4 MB unpacked, so the cost is a few MB).
- **Browser is off by default**; setting `include_browser: true` and restarting fetches Chromium's headless shell at that point rather than shipping 250 MB in the tarball.
- **Upgrade/uninstall**: re-running `install.sh` upgrades in place; `aos uninstall` removes the binary and unit but keeps `/home/aos`, `/var/lib/aos` and the config unless `--purge`.
- **Docs site**: Astro Starlight, in this repo, built to static files Aman hosts on his own domain. Never served by `aosd`, never in the binary.

### Settled 2026-09-17, second round

- **Repository**: `Aman123at/agentic-os`, **private**, created and pushed this session. The five commits of M5.1/M5.2/M5.3 work plus this charting are now on `main`; the tree cross-compiles clean for `linux/amd64`.
- **One name: `agentic-os`.** It already matches the Go module's repo segment and the image name in `compose.yaml`. Consequence: the module path's *owner* segment is still wrong (`amantiwari`, not `Aman123at`) — see *Naming*.
- **Password hashing**: Go's stdlib `crypto/pbkdf2`, not `golang.org/x/crypto`/argon2id. ADR-0002 is proud of the single binary; this costs zero new dependencies.
- **YAML**: `gopkg.in/yaml.v3` is accepted as a new dependency. There is no stdlib option, and a file users hand-edit needs comments.
- **Widening Agents to `/` is approved.** The Landlock ruleset only needs to walk the *ancestor chain* of each Protected Path, not the whole filesystem, so the cost concern in *Widening the filesystem to the whole VPS* is bounded by construction — but it is verifiable only on the VPS.

### From resolved tickets

<!-- one line per closed ticket, newest last -->

- [The model catalogue: which models, and which support reasoning effort](issues/04-model-catalogue.md): the catalogue must ship as data — `GET /v1/models` reports nothing about reasoning effort — and it seeds `/var/lib/aos/models.yaml`, open rather than a strict allow-list. Found a live defect on the way: the fixed effort list in `internal/settings/settings.go:74` is missing `max`, still offers the legacy `minimal`, and is per-model in reality, where a wrong value is an HTTP 400 rather than a clamp.

- [Release engineering: multi-arch binaries and GitHub Releases](issues/09-release-engineering.md): a hand-written `release` stage in `tools/ci`, not GoReleaser, because the existing Dockerfile compiles Go itself and GoReleaser would need a second one that drifts. Version-less asset names let `install.sh` avoid the GitHub API and its per-IP rate limit entirely. Two traps found: `-ldflags -X` silently cannot write `Version` because it is a `const`, and the first tag must be `v0.1.0` — a `-m6` suffix makes GitHub treat it as a prerelease and `/releases/latest` 404s.

- [Naming: module path, repo, binary and Docker Hub namespace](issues/17-naming.md): one name, `agentic-os`, everywhere. The repo is `Aman123at/agentic-os` (private, pushed). Release assets are `agentic-os-linux-<arch>.tar.gz`, version-less so `/releases/latest/download/` resolves. Docker Hub is `aman123at/agentic-os`. Binaries stay `aos` and `aosd`; the product stays "Agentic OS". One loose end raised for Aman: the module path's owner segment and the stated install URL path (`/agent-os/`) both still say something else.

## Not yet specified

- **Data migration for existing Compose users.** Someone running the Compose image today has a home volume, a SQLite database and an Install Ledger. Whether the native install can adopt that state, and how, is unclear until the native layout is fixed.
- **Non-Ubuntu and non-systemd Linux.** Aman said "Ubuntu or any linux distribution". Debian is free; RHEL-family and Alpine (musl, OpenRC, no systemd) are not. How far `install.sh` should stretch, and where it should refuse cleanly, needs the packaging decisions first.
- **What `aos doctor` means without a container.** Half its checks (`--host-check`, forwarding, the secret mount) describe a Host/Machine boundary that no longer exists natively. The replacement check-list depends on the sandbox and systemd decisions.
- **Performance targets (§16) on a VPS.** The existing targets assume Docker Desktop on Apple Silicon. A 1-vCPU VPS will miss several. Which targets apply natively, and what they become, can't be set until the install exists to measure.
- **Finder against pseudo-filesystems and huge directories.** `/proc`, `/sys` and `/usr/lib` will be reachable for the first time. The shape of the fix (exclusions, lazy counting, a depth guard) depends on how the filesystem widening lands.
- **Whether `/etc` Checkpoints still earn their place** once Replay is off and the box is persistent.
- **Whether the milestone-derived version scheme survives.** `version_linux_test.go` ties `Version` to the newest `### M<n>` heading in the plan, which fights tag-derived release versions. Reconciling them is now unblocked — naming and release engineering are both settled.
- **When the module path gets renamed.** `github.com/amantiwari/agentic-os` should be `github.com/Aman123at/agentic-os`. Mechanical but tree-wide, and it wants its own commit before the first tag, not folded into M6's work.
- **Observability of a long-running VPS service** — log rotation, journal size, what `aos service status` shows about uptime and restarts.

## Out of scope

Ruled beyond this destination. These do not graduate; they would need a new effort.

- **macOS and Windows native installs.** Aman: "do not care about other OS."
- **Email or SMS password recovery.** Explicitly excluded — no mail or OTP service, so no forgotten-password flow at all.
- **Multiple users, roles or permissions.** One user, one password.
- **TLS terminated by `aosd` itself.** The user puts nginx in front; certificates are their business.
- **Model providers other than OpenAI.** Unchanged from the v1 plan's non-goals.
