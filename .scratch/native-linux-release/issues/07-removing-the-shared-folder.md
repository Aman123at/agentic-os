# Removing the Shared Folder

Type: grilling
Status: resolved
Blocked by: —

## Question

Aman: "Remove the concept of Shared folder, it doesn't required at this time" — everywhere, including Compose.

It is referenced in roughly thirty files: `compose.yaml` (the `/shared` volume and `AOS_SHARED_DIR`), `CONTEXT.md` (the glossary entry), `README.md`, `internal/sandbox/policy.go` (`Layout.Shared`, the Protected list), `internal/sandbox/home_linux.go` (the `~/Shared` symlink), `internal/files/ops.go` and `trash.go` (`Ops.Shared` and the per-mount Trash), `internal/policy/protected.go`, `internal/profile`, `internal/agent/instructions.go`, `internal/cli/cli.go`, `internal/api/watch.go`, `tools/hostcheck`, `tools/ci`, the e2e harnesses and several test files.

Settle:

1. **Is `Ops.Shared` deleted, or generalised?** The per-mount `.Trash-<uid>` logic it carries is exactly what ticket *Widening the filesystem to the whole VPS* needs for `/mnt/data`. Deleting the field but keeping the behaviour is probably right.
2. **Existing installs.** A Compose user has files in `/shared` and a `~/Shared` symlink. Does the upgrade leave them, move them, or just stop managing them? Removing the symlink silently could look like data loss.
3. **What replaces it for Compose users**, who now have no way to move files in or out except the Desktop's upload/download buttons. Is that acceptable, or does Compose keep a bind mount by another name?
4. **Order of work** — whether this lands before or after the filesystem widening, since both touch `Layout` and the Protected list, and doing them in the wrong order means writing the same code twice.
5. **The glossary entry** in `CONTEXT.md` — deleted, or kept with a note that it is gone (coordinate with *The glossary after Machine and Host collapse*).


## Constraint from *Widening the filesystem to the whole VPS* (2026-09-17)

**Do not delete the Shared Folder's Trash code — move it.** `internal/files/ops.go:26` implements the freedesktop `.Trash-<uid>`-at-the-mount-root rule for the Shared Folder, and that is exactly the mechanism a whole-filesystem Finder needs for deleting outside `/home/aos`. It generalises to "the mount point of the path being deleted". Removing the Shared Folder must carry that code across rather than take it with it.

## Constraint from *Which decisions become ADRs* (resolved 2026-09-17)

This ticket's outcome edits **ADR-0004's home-layout list** in place — `~/Shared` is a root-owned symlink to `/shared` in the layout the sticky bit protects, and 0004's `## Ruleset` section is the spec the sandbox code cites. Resolve it knowing the ADR text changes with it; 0004's M6 amendment cannot be finished until this closes.

## Answer

Resolved 2026-09-17. **The Shared Folder is deleted as a concept and replaced by nothing.** What it carried — trashing a file that does not live on home's filesystem — stops being a configured folder and becomes a property of the filesystem itself.

### 1. `Ops.Shared` is deleted, not renamed

The field, `Layout.Shared`, and the `"shared"` trash key all go. Trash selection becomes a device comparison:

- `st_dev` of the path (its parent, for a path being created) equals `st_dev` of `Home` → the freedesktop **home Trash**, `~/.local/share/Trash`.
- Otherwise → walk up from the path until `st_dev` changes; the last directory with the original device is the mount root, and the Trash is `.Trash-<uid>` there.

This is strictly smaller than what it replaces, and it is correct where the current code is not. `within(path, o.Shared)` (`internal/files/trash.go:55`) compares path prefixes, but `rename()` fails across devices, not across prefixes.

**The consequence that matters on the target machine:** a stock Ubuntu VPS is one filesystem, so `/etc/nginx.conf` and `/home/aos` share a device and deleting the former is a plain rename into the home Trash. Nothing creates `.Trash-<uid>` at `/`, nothing litters the server's root, and the "mount root is not writable" refusal that *Widening the filesystem* anticipated almost never fires. Under a prefix rule it would have fired on **every** delete outside home, because `/` is `root:root 0755` and uid `aos` cannot create `/.Trash-1000`.

### 2. The item ID stops being an enum

`TrashItem.ID` is `"home/<name>"` or `"shared/<name>"` today, resolved by `trashByID` against a fixed two-element list — so the ID scheme, not just the field, blocks generalisation.

**ID becomes the absolute path of the trashed entry** (`<trash>/files/<name>`), validated structurally on the way back in: `<name>` contains no separator and is not `.` or `..`; its parent is `…/files` with a sibling `info/`; its grandparent is either the home Trash or a `.Trash-<uid>` whose uid is this one. No list to consult, no key to invent. IDs are listed and then used inside one Desktop session, so there is nothing to migrate.

### 3. `ListTrash` enumerates mounts, carefully

The home Trash, plus `.Trash-<uid>` on each real mount read from `/proc/self/mountinfo`. Pseudo-filesystems are skipped, and so are **network filesystems (`nfs`, `cifs`, `fuse.sshfs`) deliberately** — a `stat` on a dead NFS mount blocks indefinitely and would hang the Trash window with it. On a typical VPS this is one or two stats.

### 4. An unwritable mount root refuses, and says what to do instead

*Widening the filesystem* settled the refusal; it now gets an exit. The Tool error names the filesystem and states that the item can be deleted permanently instead, and Finder offers **Delete Permanently** behind a confirmation in that case. With the device rule this is a rare path rather than the normal one.

### 5. `PrepareHome` removes the symlink it used to create

**Deleting the creating code is not enough and would be a silent defect.** `~/Shared` is a *root-owned* symlink in a home that is `root:aos 1775`; the sticky bit exists so that uid `aos` cannot remove root's symlinks. Every existing volume would keep a dangling link that neither the user nor an Agent can ever delete.

So `PrepareHome` gains a removal step, in the shape the same function already uses for its pre-M1 migration (`internal/sandbox/home_linux.go:52-61`): a symlink at `~/Shared` pointing at `/shared` is removed; a real folder, or a symlink pointing elsewhere, is moved aside as `.from-home-<stamp>` with a returned note. Idempotent, one `Lstat` per start, and it stays indefinitely.

### 6. Compose gets nothing named

The bind mount line goes. The documentation shows a plain bind mount into the home volume instead — `-v ~/aos-files:/home/aos/Files` — carrying **no special status**: no `Layout` field, no Protected entry, no root-owned symlink, no Trash key. Agents write it like any other folder, and its cross-device deletes are handled by §1 without knowing it exists. Beyond that: the Desktop's upload and download buttons, and natively `scp`/`rsync`, which is what anyone with a VPS reaches for first.

M0's finding F2 does not return. It bit because `~/Shared` was *Protected* while sitting beneath a Writable tree, so home's rights reached into it. An ordinary writable folder is the intended outcome, not a hazard.

**A support burden disappears with it.** `/shared` is the only bind mount in `compose.yaml`, and `README.md:22-26` exists solely to explain that Docker Desktop hangs the container in "Created" when the repository sits under `~/Desktop`, `~/Documents` or `~/Downloads` — because of that line. Two of the three documented workarounds go with it.

### 7. Order of work: this lands before the widening

Both touch `Layout` and `Policy()`. The subtraction goes first, so *Widening the filesystem* rewrites one smaller function instead of rewriting the widened one twice, and the hostcheck probes are deleted rather than ported and then deleted. **The Trash generalisation (§1–§4) ships inside this sub-task**, not the widening — removing the Shared Folder without it would delete the only cross-filesystem Trash the product has.

### 8. Four lists, one commit

"Shared is Protected" is asserted in four independent places, and `policy.DefaultPaths`' own comment says they must never disagree:

- `internal/sandbox/policy.go:13,18,55` — `Layout.Shared` and its `Policy()` Protected entry (kernel-enforced).
- `internal/policy/protected.go:12` — `/shared` in `systemPaths` (policy-enforced).
- `internal/daemon/support_linux.go:137` — the `"/shared (the Shared Folder)"` special case in the API's Protected list.
- `tools/hostcheck/landlock_linux.go:45-77,169-176,208,223` — four asserted denials and the home-layout link check.

Text follows in the same commit: `internal/agent/instructions.go:39,49`, `internal/profile/profile.go:87`, `internal/cli/cli.go:66`, the `proto/aos/v1/services.proto:727` comment, `desktop/src/apps/finder/fs.ts:18-64` (the `shared` Place and the `/shared` breadcrumb and `parent()` special cases — a simplification, since *Widening the filesystem* replaces Places with Home, Downloads, Filesystem and Trash), and `README.md:22-26`.

**The `CONTEXT.md` glossary entry is deleted**, not tombstoned in place. *The glossary after Machine and Host collapse* gains a short "Retired terms" line at the foot of the glossary so the word stays findable from old commits and docs.

### 9. Activity Monitor

`internal/daemon/daemon_linux.go:112` samples `Home`, `Shared` and `StateDir`. Dropping the field would silently shrink a user-visible display, and the row a VPS owner actually wants was never in it. `Disks` becomes **Home, `StateDir` and `/`, deduplicated by device**, so a single-filesystem server shows one row instead of three copies of the same number.

### 10. Harnesses

`tools/ci/main.go:396,410` and `tools/e2e/e2e_test.go:185,210` drop the scratch `shared` directory and `AOS_SHARED_DIR`. `internal/sandbox/plan_test.go:146-157` loses its fixture. `internal/files/trash_test.go` is rewritten against two temp directories on one device, plus a second device where the platform allows one; the device-resolution helper is tested directly so the cross-device branch is covered on a machine with one filesystem.

### ADR consequence

ADR-0004's home-layout list loses `~/Shared` and the sentence explaining why it is mounted outside home. Recorded for *Which decisions become ADRs*, whose 0004 amendment was waiting on this ticket.
