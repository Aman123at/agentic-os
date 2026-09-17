# install.sh, written and read

Type: prototype
Status: open
Blocked by: 09, 17

## Question

Build a throwaway `install.sh` so its shape can be argued over concretely rather than in the abstract, then decide what the real one does.

Target: `curl -fsSL https://amantiwari.co.in/agent-os/install.sh | sh` on a fresh Ubuntu VPS, non-interactive, no prompts, no toolchain required.

The prototype should make these concrete and reviewable:

1. Shell dialect (POSIX `sh` versus `bash`), and `set -eu` discipline — a partially-executed installer is how boxes get broken.
2. Preflight: OS and architecture detection, kernel/Landlock check, disk space, whether `curl`/`tar` exist, refusing unsupported hosts with a clear message.
3. Resolving "latest release", downloading, verifying the checksum, and extracting.
4. Creating the `aos` user and group, `/home/aos`, `/var/lib/aos`, `/run/aos`, `/etc/aos/config.yml` with a commented default template, and the sudoers entry.
5. Installing the systemd unit and starting the service.
6. Idempotence and upgrade-in-place: what a second run does, and what it refuses to do while a Task is running.
7. Failure and rollback: what is left behind when step 5 fails, and whether a half-install can be detected and repaired.
8. What it prints at the end — the URL, the config path, and the exact next commands.
9. `aos uninstall`, keeping `/home/aos`, `/var/lib/aos` and the config unless `--purge`, printing exactly what it will delete.

Link the prototype from this ticket. It is throwaway — the real script is written during M6 implementation, not here.

## Constraint from *The systemd unit and the `aos service` lifecycle* (resolved 2026-09-17)

- Write the new binary to a temp path and `rename()` it into place. Writing over a running executable gets `ETXTBSY`.
- `systemctl daemon-reload` after writing `/etc/systemd/system/aos.service`; `systemctl enable --now aos` on install.
- Refuse cleanly when `/run/systemd/system` is absent, naming Docker Compose as the supported alternative. No PID-file fallback.
- The unit is `Type=notify`, so `systemctl start` returns only once the listener is up — the URL the script prints is safe to print immediately.
- The port scanned on first start is written back into `config.yml`, so the script reads the port from there rather than assuming 7700.
