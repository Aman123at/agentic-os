# Agents run unprivileged and Landlock-confined inside the single container

`aosd` runs as root and alone holds the OpenAI API key (read from a Docker secret file, never an environment variable). Every command an Agent runs executes as the unprivileged `aos` user with `no_new_privs` set, inside a Landlock ruleset that makes Protected Paths read-only and the secrets directory unreadable for that process tree, so no script can bypass the guard. Anything needing root or touching a Protected Path must go through a Tool, where Autonomy and Approval are enforced. We chose this over a separate Machine container because it keeps the single-binary shape (ADR-0002) and avoids a network hop on every Terminal keystroke, while still keeping the key and Protected Paths out of an Agent's reach.

## Consequences

- The user's own Sessions also run as `aos` but unconfined, with `sudo`. Agents cannot use `sudo` even though they share the uid, because `no_new_privs` makes the kernel ignore setuid binaries. Landlock also stops a confined process from ptrace-ing an unconfined one.
- Requires a Host kernel with Landlock enabled. Confirmed on Docker Desktop for macOS (7.0.12-linuxkit: `capability,bpf,landlock`) and in Microsoft's WSL2 kernel config used by Docker Desktop for Windows (`CONFIG_SECURITY_LANDLOCK=y`, x86 and arm64); Docker's default seccomp profile permits the `landlock_*` syscalls. Linux Hosts vary by distribution and kernel age.
- On a Host without Landlock, AOS starts with rule-based protection only, shows a permanent warning, and treats `AOS_AUTONOMY=auto` as `confirm-risky`; `AOS_REQUIRE_LANDLOCK=true` makes it refuse to start instead. `no_new_privs` still blocks `sudo` there.
- The exact allow-list shape of the ruleset (Landlock grants access; it cannot carve exceptions out of a granted directory) must be validated by a spike before building on it.

## Considered Options

- **Two containers (aosd + Machine)**: stronger isolation, rejected for v1 on complexity and per-keystroke latency.
- **Pattern-matching shell commands only**: rejected as sole defence; trivially bypassed by any script. Kept as an early-warning layer that asks before the kernel would deny.
