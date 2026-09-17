# Removing the Shared Folder

Type: grilling
Status: open
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
