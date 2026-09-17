# Widening the filesystem to the whole VPS

Type: grilling
Status: resolved
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


## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

### Four findings from the sandbox code

1. **Half the decision is already true.** `Plan` calls `carve(fsys, "/", hidden, Read, List, &rs)` at `internal/sandbox/plan.go:100` — Agents already have Read+List on the whole filesystem, carved around Hidden. **Only `Writable` changes.**
2. **The walk cost is a non-issue, and the reason is precise.** `carve` recurses *only* into directories that contain an excluded path (`internal/sandbox/plan.go:177`); it never touches the rest of the tree. So the walk is bounded by the ancestor chains of the exclusions. **This holds on one condition, which becomes a hard rule: no exclusion may ever live under `/proc` or `/sys`**, or carve would `ReadDir` a directory with thousands of volatile entries. Marking `/proc` *itself* Protected is safe and cheap — `carve` stops at an excluded root without descending.
3. **The real hazard is `Stale`, which the ticket did not anticipate.** `Ruleset.Stale` (`internal/sandbox/plan.go:203`) re-reads every split directory and compares signatures; any change means re-plan and re-apply in a fresh process. Widening splits `/etc`, so **every package install invalidates the ruleset**. And `/run` is *already* split today because of `/run/secrets` — harmless in a container, but on a real Ubuntu box `/run` churns constantly (`/run/user/*`, unit runtime directories, lock files).
4. **The cross-mount Trash mechanism already exists.** `internal/files/ops.go:26` implements `.Trash-<uid>` at a mount root — for the Shared Folder, which *Removing the Shared Folder* is about to delete.

### Decisions

1. **`Writable: ["/"]` with an explicit Protected list**, never a literal bare `/` — `carve` would otherwise split `/` and grant Write on every top-level entry including `/proc`, `/sys`, `/boot` and `/snap`.
   - **Hidden**: `/var/lib/aos`, `/etc/aos`.
   - **Protected**: `/boot`, `/proc`, `/sys`, `/snap`, `/root`, every other user's home, `/usr/local/lib/aos`, the `aosd` binary and the systemd unit.
   - **`/dev` stays Writable.** The Session's PTY lives there; making it read-only breaks Terminal.
2. **Accept the `/etc` split, and measure it on the VPS.** A re-plan is roughly 500 grants across `/`, `/etc`, `/home`, `/var`, `/var/lib` and `/run` — likely single-digit milliseconds, and re-planning is already the designed response to staleness. **`/run` is the one to watch**, being the volatile one. This is the one number that cannot be obtained from Aman's Mac and must come from the VPS.
3. **Constraint handed to *`/etc/aos/config.yml`: schema, validation and the `aos config` verbs*, not left as its open question 6.** `/etc/aos` is Hidden precisely because `config.yml` holds the initial password and Agents must never read it. By the same reasoning **the OpenAI key cannot live in `config.yml`** — it belongs in `/var/lib/aos/keys/openai`, which is already Hidden.
4. **Other users' homes are read from `/etc/passwd` at every plan**: the home of every uid ≥ 1000 plus root's, minus AOS's own. A static `/home/*` rule misses `/root` and homes outside `/home`. This splits `/home`, which is stable — new users are rare.
5. **Pseudo-filesystems stay browsable in Finder**, but a fixed skip-list excludes `/proc` and `/sys` from the recursive size/count walk and from the watcher: sizes there are meaningless and a recursive walk can hang.
6. **Large directories are capped server-side** at 10,000 entries with an explicit "showing the first N", rather than streaming `/usr/lib` into the window.
7. **Trash generalises rather than disappears.** The same `.Trash-<uid>` rule, resolved by the mount point of the path being deleted. **Coordinate with *Removing the Shared Folder* so this code moves instead of going away.** When the mount root is not writable, **refuse the delete with a clear message** rather than silently performing a cross-device copy.
8. **`PrepareHome` fails closed on a marker, not on a path comparison.** The installer writes a marker inside `/home/aos`; `PrepareHome` refuses to touch any home without it. Pointed at a live `/home/ubuntu` it would relocate the `authorized_keys` that is the only way into the server, so the guard must key off something it can see.
9. The `rm` shim (`internal/cli/cli.go:66`) drops `Shared` and resolves Trash by mount. The Machine Profile (`internal/profile/profile.go:87`) stops describing `~/Shared` and describes a real server instead.
