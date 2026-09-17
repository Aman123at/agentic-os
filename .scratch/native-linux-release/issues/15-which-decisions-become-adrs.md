# Which decisions become ADRs

Type: grilling
Status: open
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
