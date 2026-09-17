# Mode switching and the single binary

Type: grilling
Status: resolved
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

## Constraint from *The systemd unit and the `aos service` lifecycle* (resolved 2026-09-17)

The drain guard covers `aos daemon restart` too, not only a Mode switch. Note also that `Manager.recover` already denies every pending Approval on start (`internal/task/manager.go:140`) and interrupts awaiting-user Tasks — so any restart, attended or not, has a user-visible cost. The command spelling settled there: `aos daemon start|stop|restart|logs` plus `aos status`; `aos service <name>` stays the Services group.

## Input from *The authentication screens and account lifecycle* (resolved 2026-09-17)

**Question 6 is answered: the Mode switch is the account-creation moment, and it happens in the CLI.** Entering `ui` Mode requires `username` and `password` in `config.yml`; if `password` is unset the switch generates one, writes it back and prints it — the same rule *Reach, bind and the two Host checks* set for first start, so there is one behaviour and not two. `aosd` hashes it into `users` when it starts in `ui` Mode with no user row, and the browser never has an unclaimed state for anyone to race for. Nothing about the account is decided in the browser.

Consequence for question 5's spelling: whichever verb wins, the "write a generated password and print it" step belongs to it, alongside the drain guard.

## Answer

Settled 2026-09-17 with Aman over one grilling round ("go ahead with all your recommendations").

### Three findings that reshaped the ticket

**`cli` Mode listens on the network today.** `internal/daemon/daemon_linux.go:198,229` starts `tcp.ListenAndServe()` unconditionally; only the assets, the Desktop Tools and the login-code line sit behind `cfg.Mode == "ui"`. So "in `cli` Mode nothing is exposed" — which *Reach, bind and the two Host checks* leaned on when it decided no account is needed there — is not true of the code. Natively on `0.0.0.0` a `cli` install would answer on 7700, and because the forwarder is composed outside the authenticator (*The authentication screens*), `/port/<n>/` would serve every Agent-started port in the one Mode that has no account and so no credential that could refuse anything.

**A restart is cheaper than the ticket assumed, and the guard was aimed at the wrong states.** `internal/task/manager.go:136-155`: `recover` marks `RUNNING` **and** `AWAITING_USER` Interrupted with "aos resume continues it", denies pending Approvals, and **re-queues `QUEUED` Tasks untouched**. Queued work needs no protection, and cancelling is strictly worse than interrupting.

**The two images are 1 MB apart.** `docs/m0-findings.md:46-48` measured `cli` 498 MB and `ui` 499 MB unpacked, 151 and 152 MB compressed. The whole saving from the `cli` target is the Node stage's build *time*. Against it: two targets, two image tags, `AOS_IMAGE_MODE`, and the refusal at `cmd/aosd/main.go:45`.

### Decisions

1. **`cli` Mode starts no TCP listener at all** — the Unix socket and nothing else. This makes ticket 01's claim true rather than aspirational, removes the unauthenticated forwarder from the Mode that cannot authenticate anyone, and makes `bind` and `port` inert instead of misleading. Port forwarding and the Browser are already `ui`-only, so nothing else is lost.

2. **The `desktop` build tag and `embed_none.go` are deleted.** One embed file, always compiled, `//go:embed all:dist` unchanged. `Assets()` returns `nil` when `dist/index.html` is absent, so a plain `go build ./cmd/aosd` still produces today's honest short note instead of a Desktop that 404s — the note is extended to say the Desktop was not built and how to build it. The constraint that forces the runtime check rather than a build-time one: the Dockerfile's `go-test-run` stage never builds the Desktop, so `dist/index.html` cannot be a hard build requirement. `tools/ci` and the Dockerfile stop passing `-tags desktop`.

3. **The Dockerfile's `cli` and `ui` targets collapse into one runtime image.** `AOS_IMAGE_MODE` is deleted, and with it the refusal at `cmd/aosd/main.go:45` and the `(image built for …)` parenthetical at `internal/cli/doctor.go:31,35`. `compose.yaml` loses `target:` and tags plain `agentic-os`. Accepted cost: every Compose build runs the Node stage, which buys about 1 MB back and is covered by BuildKit's existing `/root/.npm` cache.

4. **The guard blocks on running and awaiting-user Tasks, never on queued ones.** The refusal names the Tasks and their ids. `--wait` waits for running Tasks to finish but still refuses on awaiting-user, which can sit for days. `--force` restarts anyway, prints the `aos resume <id>` line for each Task it interrupts, and says plainly that pending Approvals are denied. **It does not cancel anything**: Interrupted plus `aos resume` is what an unattended `Restart=always` already produces, and it is recoverable where Cancelled is not.

5. **The spelling is `aos mode ui` / `aos mode cli`**, with bare `aos mode` printing the current one — one word, matching `aos status`. That verb runs the guard, generates and prints the password on the way into `ui`, writes the key and restarts. **`aos config set mode=…` keeps working** (ticket 05 accepts startup-only keys rather than refusing them) but does not restart: it writes and says "restart to apply with `aos mode ui`, which checks for running Tasks first."

6. **The invariants live in `aosd`'s start path, not in the verb.** `/etc/aos/config.yml` is hand-editable and `systemctl restart aos` needs no CLI of ours, so any check that lives only in `aos mode` is decoration. Starting in `ui` Mode with no user row hashes the configured password in; with no `password` key it generates one, writes it back and logs it, exactly as first start does. One default is added so a minimal hand-written config works: **`username` defaults to `aos`** when absent, rather than refusing the start.

7. **`ui` stays the default**, natively and under Compose. `install.sh` writes `mode: ui`, so a fresh VPS install is a Desktop behind a generated password that `aos status` prints — which is the product, and what ticket 11's installer already assumes when it prints a URL. Considered and rejected: defaulting to `cli` so nothing is exposed until the user opts in — safer by a hair, at the cost of making the first run two steps.

8. **Compose parity.** `AOS_MODE` survives only as a seed for the generated `config.yml` on first start (*The config file*, decision 10). The image never knows its Mode, and `aos mode` works identically inside the container.

### The finding that was not on the list

The Agent prompt has **three** falsified sentences, not the one already booked. Besides "There is no systemd" (`internal/agent/instructions.go:38`), line 31 tells every Agent the Machine is "running in Docker on <host>", and the Machine section says: "The Machine restarts with a fresh system: only the home folder, software from install_package and /etc changes made through these Tools survive." With Replay off on a persistent server that is false **and behaviour-shaping** — it is the sentence that makes Agents cram everything into home and distrust the filesystem they can now write. `Machine.Mode` is a field of the same struct, which is why it surfaced here. The M6 sub-task rewrites the **whole Machine paragraph**, and adds a sentence in `cli` Mode saying there is no Desktop.

### Owed to other tickets

- ***Installing Chromium lazily, and Compose parity*** (12): after decision 3 the Dockerfile has one runtime stage and the `ui-browser-true` / `ui-browser-false` pair is the only remaining build-time fork. Collapsing it is that ticket's to finish.
- ***Write M6 into docs/PLAN.md*** (16): two sub-tasks — the Agent prompt paragraph, and "no TCP listener in `cli` Mode", which must land **with** the bind and forwarding work rather than after it.
- ***Which decisions become ADRs*** (15): the no-list entry stands — this is PLAN §6.1 material, not a decision record. One sentence is owed to ADR-0007's M6 section: in `cli` Mode the API has no TCP surface at all, which strengthens its thesis rather than qualifying it.
- ***The glossary after Machine and Host collapse*** (14): **Mode stops being a property of the image** and becomes a runtime setting. `CONTEXT.md`'s entry and `docs/PLAN.md` §6.1 both describe the build-target version.
