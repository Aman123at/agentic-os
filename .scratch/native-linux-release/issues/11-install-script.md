# install.sh, written and read

Type: prototype
Status: resolved
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

## Answer

Resolved 2026-09-17. Aman: "go ahead with all your recommendations."

**Prototype**: [`tools/spikes/install/`](../../../tools/spikes/install/) — `install.sh`, `uninstall-sketch.sh`, `README.md`. Throwaway, and marked so in every file. Rehearse it anywhere, including macOS:

```sh
DRY_RUN=1 sh tools/spikes/install/install.sh
```

Every mutating step goes through `run()` and every preflight refusal through `fail()`, so a dry run prints the complete sequence of changes *including the checks it would have refused on*. Written this way on purpose: the target environment cannot be exercised from Aman's Mac, so the plan had to be reviewable without one.

### The finding that outgrew the ticket

**The Docker image's `machine` stage is a product decision the installer cannot inherit.** `docker/Dockerfile:115` apt-installs ~25 packages (git, rsync, rclone, jq, python3, pipx, vim, htop, …), copies a curated Node into `/usr/local`, installs `docker/rootfs/etc/apt/apt.conf.d/10-aos-cache` — which redirects **apt's cache globally** — and runs `userdel -r ubuntu`.

In a disposable image all four are free. On a VPS: `userdel -r ubuntu` deletes the administrator; the Node copy overwrites whatever they installed; the apt redirect changes their package manager system-wide for a Replay feature that is **off** natively. So none of it carries over, and that exposes an unasked product question — *what software does a native Machine have?* `internal/agent/instructions.go` promises Agents `pipx` and `npm` on a box that may have neither.

### Smaller findings

- **`useradd --uid 1000` collides with the administrator** on every stock Ubuntu image, where uid 1000 is `ubuntu`. Dropped: `aosd` looks the user up by name (`internal/daemon/daemon_linux.go:245`), so nothing needs a pinned uid. The `uid`/`gid` config keys are vestigial for the native install.
- **The sudoers grant changes meaning off-container.** `docker/rootfs/etc/sudoers.d/aos` is `NOPASSWD:ALL` — disposable-container reasoning. Natively it means whoever gets through the password screen has passwordless root on a real server.
- **The ticket's own target URL is stale**: `/agent-os/`, which *Naming* settled as `agentic-os`.

### Decisions

**1. The installer installs nothing but `aosd`.**
It is a software install, not a box-provisioner. Agents get what the box has, and `install_package` adds the rest on demand *through the Install Ledger*, which is where the record belongs. Stated cost: the first Task needing `git` spends a turn installing it. A `--with-tools` flag that installs the image's list, opt-in and printed, is available if that friction proves real — not shipped by default.

Consequences: no `10-aos-cache` (Replay is off natively, and redirecting a real admin's apt cache is not ours to do), no Node copy, and **never** `userdel ubuntu`.

**2. The sudoers entry stays, and the documentation gets honest.**
Dropping it breaks the Desktop's Terminal, which is a real feature; narrowing it to a command list is fiction, because `run_privileged_command` already reaches root through `aosd` by design. What changes is that the docs say plainly — and the installer prints — that **the Desktop password is root on this server**. Same conclusion `bind: 0.0.0.0` already forced.

**3. `curl | sh` is documented, and the download-then-run form sits directly beneath it.**
Refusing to offer the piped form only means people write a worse one. The real mitigation is structural and is in the prototype: the whole body is a `main()` called on the **last line**, so a connection dropped mid-pipe defines functions and executes nothing. `set -eu` throughout; POSIX `sh`, not bash.

**4. A missing Landlock warns; it does not refuse.**
`require_landlock: true` already exists for anyone wanting the hard failure. Refusing by default would strand a user on an older LTS kernel with no way past it. The prototype checks both the kernel version and `/sys/kernel/security/lsm`, because a new enough kernel can still have Landlock disabled at boot.

**5. No rollback. Every step is idempotent instead.**
Download, checksum and extraction all happen **before** the first mutating step, with the temp directory trapped on every exit path, so a failure up to that point leaves nothing behind. After it, **re-running is the repair**: `install`, `ln -sf`, `useradd`-if-missing and `config.yml`-if-absent all converge. A rollback path that itself fails halfway is worse than a second run that cannot.

An upgrade never rewrites an existing `config.yml`.

**6. An upgrade refuses while a Task is running, with `FORCE=1` to override.**
Implemented in the prototype through `aos status --json`. Stopping interrupts running Tasks and denies pending Approvals (`internal/task/manager.go:140`) — recoverable via `aos resume`, but never silently.

**7. `aos uninstall` is the one place a prompt is right.**
It is irreversible, so it prints exactly what goes and what stays and requires confirmation; `--yes` for scripts. Two specifics:

- Without `--purge` the **`aos` user survives**, because `/home/aos` survives. An orphaned home owned by a recycled uid is how files become readable by the next account the system creates.
- It never touches packages the Agent installed. `aos checkpoint restore` is that verb, run beforehand. Uninstalling AOS is not a reason to uninstall nginx.

**8. Hosting: canonical at `https://raw.githubusercontent.com/Aman123at/agentic-os/main/install.sh`**, with `amantiwari.co.in` redirecting to it. Serving the script from the same static host as the documentation means a docs deploy can break installs. **The documented path becomes `/agentic-os/`** — the last loose end from *Naming*.

### Details the prototype pins down

- Version-less asset names (`agentic-os-linux-<arch>.tar.gz`), so `/releases/latest/download/` resolves with **no GitHub API call** and no exposure to the per-IP rate limit.
- `grep`-then-`sha256sum -c`, never `--ignore-missing`, which passes vacuously when the file is absent from the list.
- `install` to `aosd.new` then `mv` — writing over a running binary is `ETXTBSY`.
- `/etc/aos` and `/var/lib/aos` at `0700`, `config.yml` at `0600`, the sudoers file validated with `visudo -cf` and removed again if it does not parse.
- `.aos-home` marker written at install time, so `PrepareHome` can refuse to relocate dotfiles in a home AOS did not create.
- The commented `config.yml` template **is the product's first documentation surface** — it carries the write-back rule, the generated-password rule and the forced reset, not just keys.
- The final report prints the URL with the real IP, the config path, and the three next commands.

### Handed on

- ***The documentation site***: the "Install" page shows both invocation forms; a page must state that the Desktop password is root on the server; the path is `/agentic-os/`.
- ***Write M6 into docs/PLAN.md***: "what software a native Machine has" is a sub-task of its own, not a footnote of the installer.
