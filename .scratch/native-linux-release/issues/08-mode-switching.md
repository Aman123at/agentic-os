# Mode switching and the single binary

Type: grilling
Status: open
Blocked by: —

## Question

Settled: one binary always carries the embedded Desktop; Mode is a runtime key in `config.yml`; switching requires a restart and must wait for in-flight Agent work.

Today Mode is welded to the image: `AOS_IMAGE_MODE` is baked in, the Desktop is behind the `desktop` build tag with `embed_desktop.go` / `embed_none.go`, and `cmd/aosd/main.go:47` refuses `AOS_MODE=ui` on a `cli` image.

Settle:

1. **What happens to the `desktop` build tag and the two embed files.** Always build with the tag and delete the `none` variant, or keep the tag for a smaller optional build?
2. **The Dockerfile's `cli` and `ui` targets** exist for the same reason. Do they collapse into one image now that Mode is runtime, and does `AOS_IMAGE_MODE` disappear?
3. **What `cli` Mode actually means natively.** No HTTP listener at all, or listening on loopback with the Desktop disabled? `aos` talks over the Unix socket either way, so the listener may be pure surface area.
4. **The switch guard.** Aman: allow the switch only when no Agent is running and no Task is pending. Define "pending" precisely against the Task states in `docs/PLAN.md` §8.1 — queued, running, awaiting-user all count, but an awaiting-user Task could sit for days. `--wait` to drain and `--force` to cancel were recommended; confirm.
5. **The command spelling.** `aos config set mode=ui` plus a restart, or a dedicated `aos mode set ui` that does the check, writes the config and restarts in one step? The latter is friendlier and is what Aman originally described.
6. **Switching to `ui` with no user account yet** — hands off to *The authentication screens and account lifecycle*.
