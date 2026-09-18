# Agents run unprivileged and Landlock-confined inside the single container

`aosd` runs as root and alone holds the OpenAI API key, which it reads from a root-only file in `/var/lib/aos/keys` (delivered as a Docker secret file on a Compose install; set through System Settings or `aos config` on a native install), never an environment variable.

Every command an Agent runs executes as the unprivileged `aos` user with `no_new_privs` set, inside a Landlock ruleset that makes Protected Paths read-only and the secrets directory unreadable for that process tree. No script can bypass the guard. Anything needing root, or touching a Protected Path, must go through a Tool, where Autonomy and Approval are enforced.

We chose this over a separate Machine container. It keeps the single-binary shape (ADR-0002) and avoids a network hop on every Terminal keystroke, while still keeping the key and Protected Paths out of an Agent's reach.

## Consequences

- **User Sessions** also run as `aos`, but unconfined and with `sudo`. Agents cannot use `sudo` even though they share the uid, because `no_new_privs` makes the kernel ignore setuid binaries. Landlock also stops a confined process from ptrace-ing an unconfined one.
- **Kernel support.** A Host kernel with Landlock enabled is required. Docker's default seccomp profile permits the `landlock_*` syscalls.
  - Confirmed on Docker Desktop for macOS (7.0.12-linuxkit: `capability,bpf,landlock`).
  - Confirmed in Microsoft's WSL2 kernel config used by Docker Desktop for Windows (`CONFIG_SECURITY_LANDLOCK=y`, x86 and arm64).
  - Linux Hosts vary by distribution and kernel age.
- **Hosts without Landlock.** AOS starts with rule-based protection only, shows a permanent warning, and treats `AOS_AUTONOMY=auto` as `confirm-risky`. `AOS_REQUIRE_LANDLOCK=true` makes it refuse to start instead. `no_new_privs` still blocks `sudo` there.

## Ruleset (decided 2026-09-14 after M0; see docs/m0-findings.md)

Landlock grants access to whole trees and cannot carve an exception out of a granted folder. An early design split home into per-entry grants around `~/.ssh` and friends. That left home itself create-only, so `git clone … ~/repo` failed within the command that created the folder (finding F1). The Protected dotfiles therefore move out of the tree Agents write:

- **Home layout:**
  - `/home/.aos-protected/` holds `ssh/`, `gnupg/`, `config/`, `bashrc`, `profile` and `bash_logout`.
  - Home (`/home/aos`) is `root:aos` mode `1775` and contains root-owned symlinks to those entries. `~/.bash_profile`, `~/.bash_login` and `~/.inputrc` are also symlinks, so Agents cannot plant them.
  - `~/Shared` is a root-owned symlink to `/shared`, the Shared Folder's mount. Mounted beneath home, home's rights would reach into it (finding F2).
  - The sticky bit means nobody but root can delete, rename or replace a symlink. `aosd` creates and repairs the layout at every start. The `aos-home` volume is mounted at `/home` so both halves persist.
- **Read:** everything except Hidden paths (`/run/secrets`, `/var/lib/aos`, other Sessions' directories). Their parents get list-only rights, so the rest of `/run`, `/var` and `/var/lib` stays readable.
- **Write:** all of home, `/tmp`, `/var/tmp`, `/dev` and the Session's own directory. A write through a protected symlink resolves outside those trees and is refused.
- **Paths the user locks** (🔒, `aos protect`) inside a Writable tree are carved out (decided 2026-09-14). Every folder from the Writable root down to the locked path is split: its other entries get full rights, and the folder itself only create rights. So once anything in home is locked, F1's limitation returns along that path, home included.
  - An Agent cannot write a new top-level folder within the command that creates it.
  - An Agent can create empty files and folders inside the locked path. It can never change or delete its content.
  - The Session is re-sandboxed before its next command whenever a split folder gains an entry.
  - When a failed command created entries in a split folder, the Tool result tells the Agent to clean up and re-run.
  - Relocating locked paths like the dotfiles was rejected: surprising real paths, root-owned parent folders, git `safe.directory` failures. Policy-only locks were rejected: a script could change locked content.
- **Rules:**
  - Symlinks are never granted, because Landlock would grant their target.
  - A Protected or Hidden path that is itself a symlink also protects its target.
  - File rules carry only file rights.
  - The sandbox helper gets an explicit environment, never `aosd`'s.
  - `no_new_privs` is set on every thread, before the helper executes the confined program.
- **Files Tools** run as `aos` in the confined helper with the Agent's ruleset, not as root inside `aosd`. A symlink an Agent plants cannot redirect a Tool into root file access.
- **Approved calls on Protected Paths** run as a one-off confined process, with the ruleset widened to exactly the approved paths (for a new file, a delete or a move: its folder).
  - An approved `run_command` runs outside the persistent Session, in its current folder with a fresh environment.
  - Task-scoped grants never widen the ruleset.
- **Pattern-based Protected Paths** are not kernel-enforced: any `.env` file, and git working trees with changes the Task did not make. Landlock only protects paths that exist when rules are built. Policy asks for Approval when a Tool or a recognised shell command would change or delete them; a script can still change them.
- **Verified in M0**, by `aos doctor --host-check` on Docker Desktop for macOS (Landlock ABI 8):
  - Writing, truncating, deleting, renaming or hard-linking a Protected file is refused, from shell and from Python.
  - Replacing a protected symlink is refused.
  - Git, venvs and `rm -rf` work in brand-new top-level folders in one command.
  - Reading Hidden paths is refused, and so is `sudo`.
  - Reading a User Session's `/proc/<pid>/environ` and signalling it (ABI ≥ 6) are refused.
  - User Sessions are unaffected.
- **Not covered by Landlock** (accepted, documented residual risks):
  - `chmod`/`touch` on files the Agent's uid owns.
  - Unprotected dotfiles such as `~/.local/bin`. The Session `PATH` lists system folders first.
  - Files an Agent writes that the user later executes unconfined, such as git hooks or scripts.

## Widening to the whole filesystem (native install, M6; decided 2026-09-17)

This section is the spec the sandbox code cites, so it is edited in place rather
than forked into a new ADR. On a **Compose install** the ruleset above is
unchanged. On a **native install** (ADR-0009) the Writable set widens, because the
Machine is the user's real server and confining Agents to home would defeat the
point:

- **Only `Writable` changes; reading is already `/`.** `Plan` carves Read+List from
  `/` today, so nothing about reading widens. `Writable` becomes **`/` minus an
  explicit Protected list**: `/boot`, `/proc`, `/sys`, `/snap`, `/root`, other
  users' homes, and AOS's own binaries and systemd unit. `/var/lib/aos` and
  `/etc/aos` stay Hidden; `/dev` stays Writable. Other users' homes join the
  built-in Protected list.
- **The widening is gated by one startup key.** `filesystem` in `config.yml`
  (`AOS_FILESYSTEM`) is `home` by default — the Compose ruleset, unchanged — and
  `install.sh` sets it to `host` on a native install, where `agentPolicy` swaps
  `Writable` for the list above. Defaulting to `home` fails safe: a partial or
  misconfigured install never hands Agents write access to the whole box.
- **The walk is bounded by construction.** `carve` descends only into directories
  that contain an exclusion, so the cost is the size of the ancestor chains of the
  Protected paths, not the filesystem. This makes **"no exclusion may live under
  `/proc` or `/sys`" a hard rule** — an exclusion there would force a descent into
  a pseudo-filesystem.
- **The re-plan hazard.** `/etc` becomes a split directory, so every package
  install re-plans the ruleset, and `/run` churns on a real server in a way it
  never did in a container. `Ruleset.Stale` drives the re-plan; its cost and the
  churn rate are measurable only on the VPS (reported by Aman).
- **The Browser gets its own ruleset**, not the Agent's (ADR-0008), or the widening
  would hand an unsandboxed Chromium write access to the whole server. It lands
  before or with the widening.
- **`PrepareHome` must never relocate dotfiles in a home AOS did not create.** AOS
  owns `/home/aos`, made by the installer; pointed at a live `/home/ubuntu` the
  relocation would move the `authorized_keys` that is the only way into the server.

## Considered Options

- **Two containers (aosd + Machine)**: stronger isolation, rejected for v1 on complexity and per-keystroke latency.
- **Pattern-matching shell commands only**: rejected as the sole defence, since any script bypasses it. Kept as an early-warning layer that asks before the kernel would deny.
- **Carve-outs around dotfiles in home, re-planned between commands**: rejected after M0. Common one-liners that create a folder and write into it would fail.
- **Per-command rulesets**: rejected. The persistent PTY bash performs redirections itself, so it must carry the narrow ruleset, and Landlock never widens a running process.
- **Approved calls unconfined**: rejected. An approved command could also change every other Protected Path.
