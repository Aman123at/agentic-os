# Widening the filesystem to the whole VPS

Type: grilling
Status: open
Blocked by: —

## Question

Settled: Agents read the whole filesystem and may write outside `/home/aos`, with Protected Paths asking first; other users' homes join the built-in Protected list; `PrepareHome` never runs against a home AOS did not create; Finder's Places become Home, Downloads, Filesystem (`/`) and Trash.

Work out what that actually means in the code:

1. **The Landlock ruleset.** `Layout.Policy()` (`internal/sandbox/policy.go:57`) currently grants `Writable: /home/aos, /tmp, /var/tmp, /dev` and `Hidden: /run/secrets, /var/lib/aos`. Widening `Writable` to `/` with Protected carve-outs is supported by `internal/sandbox/plan.go`, but the ruleset is built by walking the filesystem — walking `/` on a real server, including `/proc` and network mounts, needs checking for cost and for the ruleset's path-count limits.
2. **Which paths become Hidden.** `/etc/aos` (the config and possibly the key), `/var/lib/aos`, and anything else Agents must not read.
3. **Discovering other users' homes.** Enumerated from `/etc/passwd` at startup, or a static `/home/*` rule? What about `/root`, and homes outside `/home`?
4. **Pseudo-filesystems.** `/proc`, `/sys`, `/dev`, `/run` become browsable for the first time. Excluded from Finder, shown read-only, or left alone? Sizes and counts there are meaningless and a recursive walk can hang.
5. **Huge directories.** Finder will meet `/usr/lib` and friends. `VirtualList` handles rendering; the server-side listing, sorting and the watcher need checking.
6. **Trash across mount points.** `~/.local/share/Trash` only works on one filesystem; deleting from `/mnt/data` currently becomes a cross-device copy. The freedesktop rule is a `.Trash-<uid>` at the mount root — the same pattern the Shared Folder used. Confirm and specify, including what happens when the mount root is not writable.
7. **The `rm` shim** (`internal/cli/cli.go`) hardcodes `Home` and `Shared`; it needs the new layout.
8. **The Machine Profile** (`internal/profile`) describes "key folders" to every Agent. On a real server that description is different and much larger.
