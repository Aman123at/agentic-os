---
status: accepted
---

# The Machine is the host: a native Linux install

On the primary shape, the Machine is the Ubuntu server itself, not a container on
it. A one-line `install.sh` places prebuilt `aosd` and `aos` binaries in
`/usr/local/bin`, registers a systemd unit, and starts the Daemon; the Desktop is
reached over the network at `http://<ip>:7700` behind a password. Agents act on
the real filesystem, the real process table and the real network of the VPS.
Docker Compose stays as a **sandboxed alternative**, built from the same single
binary (ADR-0002); the documentation states plainly that only the native install
reaches the real host.

This amends the container assumption that ran through ADRs 0003–0008 ("the
container is the boundary", "a container has no systemd", "recreating the
container discards state"). Each of those is edited to say which shape it means.

## Consequences

- **systemd owns the Daemon, not the Services.** `aos.service` is a systemd unit
  (`Type=notify`, `Restart=always` with a start-limit, `KillMode=mixed`,
  `TimeoutStopSec=60s`). The Services an Agent creates are **not** systemd units;
  `aosd` still supervises them (ADR-0005). The glossary keeps Daemon and Service
  distinct for exactly this reason.
- **No systemd hardening directives on the unit, on purpose.** systemd would
  confine `aosd` and every descendant indiscriminately, where Landlock plus uid
  separation confines each Agent process precisely (ADR-0004). The unit carries
  the argument in a comment so a future reader does not "harden" it and break the
  per-process model. `install.sh` refuses cleanly on a box without systemd
  (`/run/systemd/system` absent), naming Compose as the alternative.
- **The `aos` user is in sudoers with `NOPASSWD:ALL`.** So the Desktop password is
  effectively root on the VPS. Kept deliberately for a single-user personal
  server, but said out loud in the docs and README. Agents are still confined:
  `no_new_privs` blocks `sudo` even though they share the uid.
- **AOS owns its own home.** The installer creates `/home/aos`. `PrepareHome`'s
  dotfile relocation must **never** run against a home AOS did not create — pointed
  at a live `/home/ubuntu` it would move the `authorized_keys` that is the only way
  into the server.
- **The bind is public by default** (`0.0.0.0`), so a password, not a loopback
  address, is what stands between the internet and the Machine (ADR-0007). TLS and
  a reverse proxy are the operator's own business, out of scope.
- **Replay is off at boot** on a persistent server (ADR-0003); the Install Ledger
  and Checkpoint/Restore stay, user-initiated.
- **Low ports** follow the kernel default (`ip_unprivileged_port_start=1024`),
  unlike the container where Docker sets it to `0`. Agents use ports ≥ 1024 or a
  proxy unless the operator lowers it.
- **Distribution is a software install, not a build.** Prebuilt release tarballs
  for `linux/amd64` and `linux/arm64`; no Go, Node or toolchain on the VPS.

## Considered Options

- **Compose only, as before:** rejected as the primary shape — a container cannot
  reach the user's real server, which is the whole point of "an OS for Agents".
- **A distro package (`.deb`/`.rpm`):** rejected for v1; a single static tarball
  plus a POSIX `install.sh` covers Ubuntu without per-distro packaging, and the
  installer is idempotent so re-running is the upgrade and the repair.
- **systemd hardening on the unit:** rejected; it would duplicate and coarsen the
  Landlock model and break the Agents' own confinement boundaries.
- **A dedicated non-root service user with selective sudo:** rejected for v1's
  single-user model; revisit if multi-user ever lands (out of scope).
