# Which decisions become ADRs

Type: grilling
Status: resolved
Blocked by: 01, 02, 06

## Question

`docs/adr/` holds eight ADRs and `docs/PLAN.md` §20 indexes them. This effort overturns at least one and adds decisions that are hard to reverse, surprising without context, and the result of real trade-offs — the three tests for an ADR being worth writing.

Settle which get written, and what each says:

1. **Native host install as a first-class deployment.** ADR-0004 ("Unprivileged, Landlock-confined Agents in one container") assumes a container throughout. Amend it, or add a new one that supersedes part of it?
2. **Username/password + JWT replacing the access token and login code.** This directly revises **ADR-0007** ("API always authenticated, even on localhost") and changes the DNS-rebinding posture. Amendment or supersession — and ADR-0007 should not be left reading as current if it is not.
3. **The config file as the single source of truth**, with runtime changes written back.
4. **Replay off on a persistent filesystem** — this qualifies ADR-0003 ("Install Ledger + Checkpoints instead of persisting system folders"), whose entire reasoning is that the container filesystem is disposable.
5. **Widening the Agent writable tree to `/`** — the sharpest safety change in the whole effort, and the one a future reader will most want the reasoning for.

For each: new ADR, amendment, or nothing. Then list the numbers and titles so M6 can reference them, and note which existing ADRs need a status change.

## Input from *The systemd unit and the `aos service` lifecycle* (resolved 2026-09-17)

Two ADR-shaped decisions, both considered rejections a future reader would otherwise re-litigate:

- **Why the systemd hardening directives are absent.** Argued by layer: systemd confines `aosd` and every descendant indiscriminately; Landlock plus uid separation confines each Agent process precisely.
- **Why the control socket is root-only.** Rejecting a `docker`-style admin group as a permanent unauthenticated root-equivalent grant with no audit distinction.

Also relevant: ADR-0005 ("aosd supervises Services instead of systemd") now coexists with a real systemd on the host. It does not need reversing — `aosd` still supervises Services — but its reasoning sentence "a Docker container has no systemd" no longer describes the primary deployment, and `internal/agent/instructions.go:38` tells Agents "There is no systemd", which is now false on a native install.

## Answer

Resolved 2026-09-17. **Two new ADRs, five amended, an explicit no-list, and two code sub-tasks that fall out.** No ADR prose is written by this ticket — it decides the set, the shape and the homes; the files are written in the same commit as M6.

### The rule

> **A new ADR when the thesis changes, or when a thesis appears that nothing covers. An amendment when the thesis holds and only the mechanism moves. An ADR that code cites as a spec is edited in place, never forked.**

The third clause is not style. `internal/sandbox/plan.go:1`, `sandbox/policy.go:9`, `files/confined_linux.go:21`, `session/agent_linux.go:264` and `tools/hostcheck/landlock_linux.go:15,30,126` all point at ADR-0004 for the Landlock ruleset. A second document restating those lists is how the spec and the code drift apart.

There is no supersession convention in this repo to follow, and there is an amendment one: ADR-0004's `## Ruleset (decided 2026-09-14 after M0)` reversed 0004's own earlier carve-out design in place, and ADR-0008 carries `## Addendum (M5.3)`. Nothing below invents supersession.

### New

**ADR-0009 — The Machine is the host: a native Linux install.**
The umbrella the container-era ADRs point at for "what changes natively". Carries: Machine == Host on a VPS, with Compose demoted to the sandboxed alternative; systemd supervises `aosd`, `aosd` still supervises Services; **why the systemd hardening directives are absent**, by the layer argument — systemd confines `aosd` and every descendant indiscriminately, Landlock plus uid separation confines each Agent process precisely; the `aos` user in `NOPASSWD:ALL` sudoers, which makes the Desktop password root on the user's own server; and the installer installing nothing but `aosd`, because the image's `machine` stage is free in a disposable image and unacceptable on someone's box.

**ADR-0010 — Configuration lives in one file, and runtime writes back to it.**
New thesis; nothing in the eight covers configuration. Carries: `/etc/aos/config.yml` root-owned `0600`, deliberately outside the Agents' writable tree; the **deletion of the SQLite settings table** that was the real source of truth until now (`internal/settings/settings.go:1`); write-back as the direction of travel, so a restart never silently reverts a setting; a malformed file or an unknown key refusing the start rather than falling back to defaults; and the inversion that follows — the "the file takes over on restart and resets your runtime settings" warning is **false**, and the documentation says the opposite.

### Amended

**ADR-0007 — The aosd API always requires authentication, even on localhost.** Number and title keep: the thesis gets *more* true when the bind becomes `0.0.0.0`. Its **opening paragraph is rewritten, not appended to** — every mechanism it names (generated access token, one-time sign-in link, HttpOnly cookie, Host check, an Agent-rejecting socket) is gone, and a reader who stops after paragraph one must not be misled. Then `## M6: passwords, JWT and a public bind`: username+password in SQLite, one user; 15-minute access in memory, 30-day refresh in `localStorage` (XSS-readable, stated plainly), rotated on use with family revocation; the single-use 30-second ticket that authenticates every browser-initiated load; bind `0.0.0.0` as a direct continuation of 0007's own "binding to 127.0.0.1 does not stop…" argument; both Host checks deleted because the Origin check was always the real CSRF defence; `http://IP:7700` not being a secure context; and the control socket going `0600` with an explicit uid check — that socket is already a sentence in this ADR.

**ADR-0004 — Unprivileged, Landlock-confined Agents.** A dated `## Widening beyond the container (M6)` section carrying **both the reasoning and the new lists**: Writable becomes `/` minus an explicit Protected list, the Protected/Hidden sets, the hard rule that **no exclusion may live under `/proc` or `/sys`** (it is what bounds the walk), and `Ruleset.Stale` re-planning as the real cost. The same section repairs the falsified sentences: the key is `/var/lib/aos/keys/openai`, **not a Docker secret file**, and `AOS_REQUIRE_LANDLOCK` / `AOS_AUTONOMY` are config keys now. This is the sharpest safety change in the effort and it gets a §20 row of its own so it is findable without reading 0004 end to end — but it does not get a second document.

**ADR-0003 — Install Ledger + Checkpoints.** Title unchanged; it stays true of the Compose deployment. A `## On a persistent host (M6)` section that **opens by naming its own falsified premise** ("Recreating the container discards everything outside volumes") rather than working around it: the Ledger is still recorded, replay at boot is off, Checkpoint and Restore become user-initiated.

**ADR-0005 — aosd supervises Services.** One paragraph, no reversal. `aosd` still supervises Services; systemd supervises `aosd`, not them. The reasoning sentence "A Docker container has no systemd" becomes true-but-partial and says so.

**ADR-0008 — Opt-in streamed Browser.** Amendment shape known, content owed to *Installing Chromium lazily, and Compose parity* (12): build-time `INCLUDE_BROWSER` becomes a config key and a runtime fetch, **and the `--no-sandbox` justification has to be rewritten** — see below.

### The finding that was not on the list

ADR-0008 says Chromium's own sandbox is off "because it needs user namespaces Docker's default seccomp profile refuses. **The container is the outer boundary.**" `internal/browser/browser.go:31` repeats it in a comment. Natively, Docker's seccomp profile is not in the way any more — so the *reason* for `--no-sandbox` evaporates — and the container that made it acceptable is gone at the same moment. That leaves a browser rendering arbitrary web pages, unsandboxed, as a uid that is in `NOPASSWD:ALL` sudoers. The decision belongs to ticket 12, which did not mention it and now carries it as a constraint. The expected answer is "turn Chromium's sandbox on natively", which is code, hence an M6 sub-task.

### Not an ADR

Recorded so it is not re-litigated. All PLAN-section material, each for the same one-line reason — reversible, cheap, or a subtraction:

- Release engineering (a hand-written `release` stage, not GoReleaser) — reversible.
- `install.sh`'s shape (`curl | sh`, no rollback, idempotent steps) — reversible.
- The Starlight documentation site — reversible.
- Naming (`agentic-os` everywhere) — settled, not contested.
- The model catalogue shipping as data — mechanism, no trade-off worth a page.
- Removing the Shared Folder — a subtraction; glossary plus PLAN.
- Mode as a runtime setting, and the Dockerfile's `cli`/`ui` targets collapsing — PLAN §6.1, not a decision record.

### Marking and indexing

- Each amended ADR gets an italic line under its title: *Amended 2026-09-17 for the native Linux install (M6); see §…*.
- **ADR-0007's lone `status: accepted` frontmatter is dropped.** It is the only frontmatter in `docs/adr/`, and the dated line does the same job where a reader will actually see it.
- **§20 gains rows**, because it is titles-only and amendments are otherwise invisible there — a reader would see "Unprivileged, Landlock-confined Agents in one container" and never learn Agents write `/`. New rows for ADR-0009 and ADR-0010, and rows naming the amended decisions (widening to `/`, passwords and JWT, Replay off on a persistent host) pointing at their sections.

### When

**In the same commit as M6's PLAN section**, so every `ADR-0009` reference in M6 resolves when Aman reads it, and one approval covers the package. Two of the texts cannot be finished before then anyway: 0004's home-layout list waits on *Removing the Shared Folder* (07), and 0008 waits on *Installing Chromium lazily* (12).

### Falls out as M6 sub-tasks, not ADRs

1. `internal/agent/instructions.go:38` tells every Agent "There is no systemd." False on the primary deployment.
2. `internal/browser/browser.go:31,36` — the `--no-sandbox` flag and the comment justifying it by the container.

## Input from *Removing the Shared Folder* (resolved 2026-09-17)

The first of 0004's two blockers is cleared. Its home-layout list loses the `~/Shared` root-owned symlink and the sentence explaining why the Shared Folder is mounted outside home (M0 finding F2). The M6 amendment now waits only on *Installing Chromium lazily* (12), which owes ADR-0008 its text.

## Input from *The authentication screens and account lifecycle* (resolved 2026-09-17)

ADR-0007's `## M6: passwords, JWT and a public bind` section gains two things the earlier list did not have:

- **The path-scoped forwarding cookie.** Forwarded Service traffic is authenticated by the same single-use ticket, exchanged for a cookie scoped to `Path=/port/<n>/`, because a third-party page cannot attach an `Authorization` header to its own sub-resources. The section must carry the argument for why this does not reopen what ticket 01 closed: the rule is that the *API* carries no automatic credential, and this cookie reaches one forwarded Service and no API endpoint. `internal/proxy/proxy.go:1` already cites ADR-0007, so it belongs there and nowhere else.
- **Family revocation closes live connections.** Revoking a refresh family closes the event streams and session WebSockets it authorised. Without it a held stream outlives the token that authorised it and "sign out" means nothing.

Found on the way and worth a sentence in the same section: **the forwarder is composed outside the authenticator** (`internal/daemon/daemon_linux.go:198`), so `/port/<n>/` is unauthenticated today and was reachable only from loopback. Deleting the Host guard without fixing this would have published every Agent-started port.

## Input from *Mode switching and the single binary* (resolved 2026-09-17)

The no-list entry stands: Mode as a runtime setting and the collapsing Dockerfile targets are PLAN §6.1 material, not a decision record. **One sentence is owed to ADR-0007's M6 section**: in `cli` Mode the API has no TCP surface at all — the listener is not started — which strengthens the ADR's thesis rather than qualifying it, and closes the gap where an unauthenticated forwarder would otherwise sit in the one Mode that has no account.
