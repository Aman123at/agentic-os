# install.sh, written and read

Type: prototype
Status: open
Blocked by: 09, 10, 17

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
