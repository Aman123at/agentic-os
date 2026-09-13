# Agents run unprivileged and Landlock-confined inside the single container

`aosd` runs as root and alone holds the OpenAI API key (read from a Docker secret file, never an environment variable). Every command an Agent runs executes as the unprivileged `aos` user with `no_new_privs` set, inside a Landlock ruleset that makes Protected Paths read-only and the secrets directory unreadable for that process tree, so no script can bypass the guard. Anything needing root or touching a Protected Path must go through a Tool, where Autonomy and Approval are enforced. We chose this over a separate Machine container because it keeps the single-binary shape (ADR-0002) and avoids a network hop on every Terminal keystroke, while still keeping the key and Protected Paths out of an Agent's reach.

## Consequences

- The user's own Sessions also run as `aos` but unconfined, with `sudo`. Agents cannot use `sudo` even though they share the uid, because `no_new_privs` makes the kernel ignore setuid binaries. Landlock also stops a confined process from ptrace-ing an unconfined one.
- Requires a Host kernel with Landlock enabled. Confirmed on Docker Desktop for macOS (7.0.12-linuxkit: `capability,bpf,landlock`) and in Microsoft's WSL2 kernel config used by Docker Desktop for Windows (`CONFIG_SECURITY_LANDLOCK=y`, x86 and arm64); Docker's default seccomp profile permits the `landlock_*` syscalls. Linux Hosts vary by distribution and kernel age.
- On a Host without Landlock, AOS starts with rule-based protection only, shows a permanent warning, and treats `AOS_AUTONOMY=auto` as `confirm-risky`; `AOS_REQUIRE_LANDLOCK=true` makes it refuse to start instead. `no_new_privs` still blocks `sudo` there.
- The exact allow-list shape of the ruleset (Landlock grants access; it cannot carve exceptions out of a granted directory) must be validated by a spike before building on it.

## Ruleset (validated in M0; see docs/m0-findings.md)

Measured on Docker Desktop for macOS (Landlock ABI 8) by `aos doctor --host-check`:

- **Read:** everything except Hidden paths (`/run/secrets`, `/var/lib/aos`). Their parents get list-only rights, so the rest of `/run`, `/var` and `/var/lib` stays readable.
- **Write:** Writable trees (home, `/tmp`, `/var/tmp`, `/dev`) minus Protected Paths. A directory that contains a Protected Path is split: its other entries get full rights, and the directory itself gets only create rights.
- **Rules:**
  - Symlinks are never granted, because Landlock would grant their target.
  - File rules carry only file rights.
  - The sandbox helper gets an explicit environment, never aosd's.
  - `no_new_privs` is always set.
- **Verified:** writing, truncating, deleting, renaming or hard-linking a Protected file is refused, from shell and from Python; reading Hidden paths is refused; `sudo` is refused; reading a User Session's `/proc/<pid>/environ` and signalling it (ABI ≥ 6) are refused; User Sessions are unaffected.
- **Not covered by Landlock:** `chmod`/`touch` on files the Agent's uid owns, and files an Agent writes that the user later executes unconfined (git hooks, scripts). Both are accepted and documented residual risks.
- **Decision pending (M0 findings F1, F2):**
  - A split directory's create-only right makes new entries in home unwritable within the command that creates them.
  - Home's create right reaches into a Shared Folder mounted beneath it.
  - Recommended: relocate the Protected dotfiles behind root-owned symlinks in a sticky, root-owned home; grant Agents full write on home; mount the Shared Folder at `/shared`.

## Considered Options

- **Two containers (aosd + Machine)**: stronger isolation, rejected for v1 on complexity and per-keystroke latency.
- **Pattern-matching shell commands only**: rejected as sole defence; trivially bypassed by any script. Kept as an early-warning layer that asks before the kernel would deny.
