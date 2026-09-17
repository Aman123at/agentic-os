# Write M6 into docs/PLAN.md

Type: task
Status: open
Blocked by: 01, 02, 03, 05, 06, 07, 08, 10, 11, 12, 13, 14, 15, 17

## Question

The destination. With every decision above settled, write the M6 section of `docs/PLAN.md` in the same voice and shape as M0–M5, and put it to Aman for approval.

It must:

1. Break into **numbered sub-tasks small enough to track one by one** — Aman asked for this explicitly, and M5.1/M5.2 are the model for the grain.
2. Give each sub-task its acceptance criterion and its tests, in the style of the existing "Tests:" paragraphs. Remember that this Mac cannot exercise a Linux VPS: the plan ships unit and logic tests, and Aman tests on real hardware and reports back.
3. Update the parts of the existing plan this effort invalidates — §2 Scope, §5 Repository layout, §6 Container and Compose, §7 Security model, §12 Services and port forwarding, §15 Cross-Host support, §16 Performance targets, §17 Testing strategy, §20 Decision log, and the §22 working agreement's Hosts row.
4. Reference the ADRs settled in *Which decisions become ADRs*.
5. State the acceptance criterion for M6 as a whole, in the form §18 already uses — something a fresh user on a fresh VPS can be measured against.
6. Note the order of work, including which sub-tasks must land before others (the Shared Folder removal and the filesystem widening both touch `Layout`).

Then stop. **No implementation code until Aman approves it.**

## Input from *install.sh, written and read* (resolved 2026-09-17)

**"What software a native Machine has" is a sub-task of its own**, not a footnote of the installer. The Docker image curates ~25 apt packages plus a pinned Node; `install.sh` installs none of them; `internal/agent/instructions.go` promises Agents `pipx` and `npm` regardless.

The prototype at `tools/spikes/install/` is the concrete reference for the installer sub-tasks. It is throwaway — M6 writes the real script.

## Input from *Which decisions become ADRs* (resolved 2026-09-17)

**The ADRs are written in the same commit as this PLAN section**, so every reference resolves when Aman reads it and one approval covers the package. The set is fixed: **new** ADR-0009 (*The Machine is the host: a native Linux install*) and ADR-0010 (*Configuration lives in one file, and runtime writes back to it*); **amended** 0003, 0004, 0005, 0007 and 0008. §20 gains rows for the new ADRs and for three amended decisions, since the table is titles-only and amendments are invisible in it otherwise.

Two M6 code sub-tasks fall out of the review and belong in the sub-task list: `internal/agent/instructions.go:38` tells Agents "There is no systemd", and `internal/browser/browser.go:31,36` justifies `--no-sandbox` by a container that no longer exists.

## Input from *Removing the Shared Folder* (resolved 2026-09-17)

**Ordering is now fixed for two sub-tasks that were flagged as colliding:** the Shared Folder removal lands *before* the filesystem widening, because it shrinks `Layout` and `Policy()` and lets the widening rewrite one smaller function instead of rewriting the widened one twice.

**The Trash generalisation belongs to the Shared Folder sub-task**, not the widening: Trash selection becomes an `st_dev` comparison rather than a path prefix, the item ID becomes a validated absolute path instead of a two-value enum, and `ListTrash` enumerates mounts from `/proc/self/mountinfo`. Sizing note: this also means `PrepareHome` needs a migration step to remove the root-owned `~/Shared` symlink, which no existing install can delete for itself.

## Input from *The authentication screens and account lifecycle* (resolved 2026-09-17)

**One sub-task exists that no ticket had booked: moving forwarded Services inside the session.** `internal/daemon/daemon_linux.go:198` composes the forwarder *outside* the authenticator, so `/port/<n>/` is unauthenticated and was safe only behind a loopback publish. It touches `internal/proxy`, `internal/api/auth.go`, the Desktop's Services surface and `tools/e2e/m2_test.go`, and it must land in the **same sub-task as deleting the two Host checks** — separately, there is a commit in between where every Agent-started port is on the public internet.

Ordering note: the Desktop auth work (three boot phases, the interceptor, the Account pane) depends on the server side of the ticket mechanism existing, so the API sub-task precedes the Desktop one.

## Input from *Mode switching and the single binary* (resolved 2026-09-17)

Two more sub-tasks:

- **No TCP listener in `cli` Mode.** `internal/daemon/daemon_linux.go:198,229` starts it unconditionally today. This must land **with** the bind and forwarding work, not after it, for the same reason the forwarder fix does: in between, a `cli` install on `0.0.0.0` publishes every Agent-started port with no account in existence to refuse anything.
- **The Agent prompt's Machine paragraph**, rewritten whole rather than line by line. Three sentences are false natively: "running in Docker on <host>" (`internal/agent/instructions.go:31`), "There is no systemd" (line 38), and "The Machine restarts with a fresh system: only the home folder … survive" — the last is behaviour-shaping, since it is what makes Agents cram everything into home and distrust the filesystem M6 opens to them.

Sizing note for the single-binary sub-task: deleting the `desktop` build tag needs `Assets()` to return nil when `dist/index.html` is absent, because the `go-test-run` stage never builds the Desktop and so cannot carry a hard build-time requirement.

## Input from *Installing Chromium lazily, and Compose parity* (resolved 2026-09-17)

Five sub-tasks, one of them order-critical:

- **Narrow the browser's Landlock ruleset** (`internal/daemon/browser_linux.go:69` stops using `d.agentPolicy()`). This must land **before or with the filesystem widening**, for the same reason the forwarder fix must land with the bind change: in between there is a commit where an unsandboxed Chromium, which Agents can point at any page, can write to `/`.
- **`aos browser install` / `remove`** — a pinned Chrome-for-Testing download with a sha256 we pin ourselves (Playwright verifies nothing), a `debug/elf` `DT_NEEDED` check against `ldconfig -p`, a 1 GB free-space refusal, download-to-`.tmp`-then-`rename`, and a write-back of `include_browser: true` on success.
- **The Dockerfile collapses to one runtime stage**, deleting `browser-dist`, `ui-browser-true`, `ui-browser-false`, `INCLUDE_BROWSER` and `PLAYWRIGHT_VERSION`. Together with *Mode switching*'s target collapse, this is the whole of the file's build-time forking. `compose.yaml` gains a named volume for `/opt/aos-browser`.
- **The `--no-sandbox` comment at `internal/browser/browser.go:31,36`** is rewritten (already booked here via *Which decisions become ADRs*); `LibDir` and its `LD_LIBRARY_PATH` are deleted with it, since `libgbm1` becomes an ordinary package once nothing is being kept out of an image.
- **`tools/ci` loses the `ui+browser` image** and gains a cheap pin-drift check against `playwright-core/browsers.json` plus an on-demand browser-install rehearsal that downloads 120 MB.

## Input from *The documentation site* (resolved 2026-09-17)

Five sub-tasks, one of them ordered against the release:

- **Make the repository public**, before the release stage is ever run. Measured: `raw.githubusercontent.com/Aman123at/agentic-os/main/…` and `/releases/latest/download/…` both return 404 while it is private, so `install.sh`'s canonical URL and the installer's own download are inert until this lands. It is a decision, not code, but it belongs in the ordered list because two other sub-tasks are dead without it.
- **`docs-site/`** — Astro 7 + Starlight 0.42, `base: "/agentic-os/"`, the fifteen-page map, deployed on tag to `https://amantiwari.co.in/agentic-os/`. Prototyped at [`tools/spikes/docs-site/`](../../../tools/spikes/docs-site/).
- **`tools/docsgen`** — the generated command reference. Needs one production change first: `rootCmd()` at `internal/cli/cli.go:40` becomes exported `Root()`. **This sub-task lands late**, after every M6 command exists, because the reference is generated from the tree it documents.
- **`tools/ci`**: the reference-drift check joins `lint` (pure Go, milliseconds, fails like `gofmt -l`); the Astro build becomes an **optional `docs` stage** named explicitly, like `live`.
- **Rewrite `README.md` whole.** Tickets 07 and 12 book two line-edits to it, but its thesis is falsified — "An Ubuntu Machine in Docker", `docker compose up --build` as the Quick start, `INCLUDE_BROWSER`, `AOS_MODE`, and a Troubleshooting section entirely about the Shared Folder. It becomes the public front door the moment the repository goes public.

Sizing note: the generator produces 22 pages against today's tree, before M6 adds `daemon`, `config`, `mode`, `model`, `user`, `browser`, `status` and `uninstall` and deletes `desktop-url`.
