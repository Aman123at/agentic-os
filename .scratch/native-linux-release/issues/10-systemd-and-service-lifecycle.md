# The systemd unit and the `aos service` lifecycle

Type: grilling
Status: resolved
Blocked by: —

## Question

`aosd` runs as root under systemd on the VPS, and `aos service start|stop|status|restart` drives it.

Settle:

1. **The unit file.** Where it is installed, `Type=`, `Restart=`, `RestartSec=`, `StopGraceTimeout` (Compose allows 30 s today), `After=network-online.target`, and whether it is enabled at boot by default.
2. **Hardening directives.** The systemd handbook says `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes` — and every one of those directly contradicts a service whose entire purpose is reaching the whole filesystem. Decide which are genuinely compatible and document why the rest are absent, so it reads as a considered choice rather than an oversight.
3. **What `aos service ...` actually does.** Shell out to `systemctl`, or talk to systemd directly? What happens when the user is not root — `sudo` automatically, or refuse with an instruction?
4. **Non-systemd hosts.** Detect and refuse cleanly, or fall back to a plain background process with a PID file? This decides how much of the "any linux distribution" promise `install.sh` can keep.
5. **`aos service status` output** — running or not, mode, port actually bound, model, whether the browser is installed, uptime, restart count, active Tasks. It is the first thing a confused user will run.
6. **Logs.** `aosd` writes to stdout with `log.Printf`, which becomes the journal. Does `aos service logs` wrap `journalctl`? What about rotation for the SQLite database and `/var/lib/aos/services` logs, which systemd does not manage.
7. **Stop with work in flight.** SIGTERM handling already exists (`cmd/aosd/main.go`); confirm what a queued or awaiting-user Task looks like after a restart, and that the guard from *Mode switching and the single binary* applies here too.
8. **Port fallback and systemd restarts.** If the chosen port moved on restart, the user's nginx config now points at the wrong one. Does a pinned port become effectively mandatory behind a proxy?

## Answer

Resolved 2026-09-17. Aman: "go ahead with your recommendations" after the grilling below.

### Findings that changed the shape of the answer

1. **`aos service` was already taken, and it meant something else.** `internal/cli/services.go:18` defines `aos service list|logs <name>|start|stop|restart <name>` — the **Services** group, ADR-0005's subject. The charted verb `aos service start|stop|status|restart` would have collided head-on: two opposite meanings separated by one argument, over a capitalised glossary term that belongs to the other one. `aos stop` is taken too (`stop --all`, stop every Agent).
2. **`aosd`'s control socket is unauthenticated and world-connectable.** `internal/daemon/daemon_linux.go:204` chmods it `0666`; `internal/api/socket_linux.go:67` authenticates nobody. It refuses only *Agent-confined* callers, via `SO_PEERCRED` plus `NoNewPrivs` — a good defence, untouched here. In a one-user container `0666` was harmless. On a VPS whose Protected list already enumerates other users' homes, every local user holds the full unauthenticated API of a root daemon.
3. **The stop budget is larger than it looks, and Compose's is already too small.** In `daemon.Run` the 20-second HTTP `Shutdown` runs inline *before* the deferred `d.tasks.Close()`, which cancels running Agents and waits, and before `d.services.Close()`. A stop is therefore 20 s + task drain + Service shutdown. `compose.yaml:17` allows `stop_grace_period: 30s`, so a busy Machine can be SIGKILLed mid-drain today.
4. **An automatic restart denies pending Approvals.** `internal/task/manager.go:140` sets every undecided approval to `DENY` with `decided_by = 'aos: restarted'`; `:150` turns running *and* awaiting-user Tasks into Interrupted with a summary pointing at `aos resume`. Correct behaviour, but it means an unattended restart silently denies whatever the user was about to approve. Restarts are not free, which is what shapes `Restart=`.
5. **Service log rotation is already solved** — `internal/service/logs.go:14` rotates at 5 MB keeping one old file, so 10 MB per Service, bounded. The ticket's rotation worry applies only to SQLite.

### Decisions

**1. The command spelling — `aos daemon`, plus `aos status`.**
`aos service <name>` keeps its current meaning untouched. `aos stop --all` keeps its. The daemon's lifecycle becomes:

- `aos daemon start|stop|restart|logs`
- `aos status` — an alias for `aos daemon status`, and the front door. It is the shortest thing a confused user can type, and it was unclaimed.

**This reverses the charted "CLI verbs" decision**, which was taken without knowing `aos service` existed. Rejected alternatives: renaming Services to `aos services` (one letter from `aos service`, opposite targets — a foot-gun); overloading by arity (ambiguous, and it breaks the domain model).

**2. The unit file.**
`/etc/systemd/system/aos.service` — the correct location for a non-packaged install, admin-editable, and already on the Landlock Protected list. Named for the service, not the binary, as `ssh.service` is for `sshd`.

- `Type=notify`. With `simple` or `exec`, systemd reports the unit active before the listener is up and `install.sh` races when it prints the URL. sd_notify is ~10 lines writing `READY=1` to `NOTIFY_SOCKET` over `unixgram` — no dependency. Sent after both listeners are serving.
- `Restart=always`, `RestartSec=2s`. Survives an OOM kill; an explicit `systemctl stop` is still not restarted.
- `StartLimitBurst=5`, `StartLimitIntervalSec=60s`. A malformed `config.yml` now refuses the start, so without a limit it becomes a hammering loop; with one it lands in `failed`, which `aos status` explains.
- `TimeoutStopSec=60s` — see finding 3. `compose.yaml`'s `stop_grace_period` is raised to match.
- `KillMode=mixed`, so `aosd` gets SIGTERM alone and stops its Services itself, rather than systemd killing the whole cgroup at once.
- `Wants=network-online.target`, `After=network-online.target`.
- `WantedBy=multi-user.target`, and `install.sh` runs `systemctl enable --now aos`. A server product that does not survive a reboot is not one.
- `RuntimeDirectory` is **not** used: `aosd` creates `/run/aos` itself (`daemon_linux.go:262`) and chowns `/run/aos/sessions` to the `aos` user.

**3. Resource directives that earn their place.**

- `OOMPolicy=continue` and `OOMScoreAdjust=-500`. The default `OOMPolicy=stop` takes the whole daemon down when a heavy Agent build is OOM-killed; these make the kernel prefer the child and keep `aosd` alive to report it.
- `LimitNOFILE=65536`, `TasksMax=infinity`.

**4. The hardening directives are absent on purpose, and the unit file says why.**
`ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `NoNewPrivileges=yes`, `MemoryDenyWriteExecute=yes`, `SystemCallFilter=@system-service` and `RestrictNamespaces=` each either contradict the product (reach the whole filesystem, install packages, share `/tmp` with the user's own shell) or break a child (`MemoryDenyWriteExecute` breaks node's JIT; `NoNewPrivileges` inherits into every Service and every apt hook).

The argument is about **layer, not inconvenience**, and it goes in a comment block inside the unit file itself, where a reader looks for the missing directives: systemd's confinement applies to `aosd` *and every descendant indiscriminately* — the wrong granularity for a process tree that is half privileged daemon and half deliberately-confined Agent. Confinement here is Landlock plus uid separation applied per Agent process (`internal/sandbox`), which is strictly tighter than anything in this list, and deliberately absent on the daemon.

**5. `aos daemon` shells out to `systemctl`.**
Not D-Bus: that is a new dependency for something guaranteed present wherever systemd is. Status parses `systemctl show aos --property=…`, whose `key=value` output is stable, never `systemctl status`.

**6. Not being root: refuse with the command to run.**
Never auto-`sudo`. Silent escalation from a tool is bad practice, and `sudo` can block on a password prompt with no tty. The message names the exact line: `sudo aos daemon start`.

**7. Socket permissions.**
The socket becomes **`0600`, root-owned**. `/run/aos` stays `0755` because Agents must traverse it to reach `/run/aos/sessions`. `Auth.Socket` gains an explicit `uid == 0` check alongside the existing `NoNewPrivs` refusal — belt and braces, since an unconfined `su - aos` shell passes the current test. The CLI turns `EACCES` into "aosd's control socket is root-only; run `sudo aos …`".

Rejected: a `docker`-style `aos-admin` group. It is a permanent unauthenticated root-equivalent grant with no audit distinction, for a machine that has one administrator.

**8. `aos status` degrades instead of refusing.**
Two sources with different privileges. Unprivileged (systemd only): active/inactive/failed, PID, uptime, restart count, and a line saying `sudo` shows the rest. Root adds, from the socket: Mode; bind address and the port actually bound, with the reachable URL; model and reasoning effort; whether the browser is installed; Landlock ABI and whether it is enforcing; running, queued and awaiting-user Task counts; config path; version. It also prints the **initial password while it is still unreset**, per *Reach, bind and the two Host checks*. `--json` for scripts.

**9. Logs.**
`aos daemon logs [-f] [-n N]` execs `journalctl -u aos` with the flags passed through. Service logs already rotate (finding 5); the journal is journald's business and is capped by `journald.conf`.

The one unbounded store left is **SQLite** — `task_steps` and the audit log grow forever on a box that runs for months. Recorded as fog rather than solved here; retention is its own decision.

**10. Non-systemd hosts: detect and refuse cleanly.**
`install.sh` checks for `/run/systemd/system` and stops with a message naming Docker Compose as the supported alternative. **No PID-file fallback** — that is a second lifecycle implementation with no boot integration, no journal and no restart policy, and Alpine fails on musl regardless. `aosd` still runs in the foreground by hand for anyone wiring their own init: documented as possible, not supported. This is the honest bound on "any linux distribution".

**11. Port fallback under a proxy: write the scanned port back into `config.yml` on first start.**
Exactly what the config write-back decision already does for every other runtime value. The port therefore moves **once**, on first start, and is pinned from then on, so an nginx config written afterwards stays correct and no proxy user has to be told to pin anything by hand. An explicit `port:` still fails loudly rather than moving.

**12. Stop with work in flight** needs no new mechanism. `Manager.recover` (`internal/task/manager.go:136`) already re-queues queued Tasks, interrupts running and awaiting-user ones with a summary pointing at `aos resume`, and denies undecided approvals. What this ticket adds is that the behaviour must be **documented**, because finding 4 makes it user-visible on a server that restarts unattended, and that the drain guard from *Mode switching and the single binary* applies to `aos daemon restart` as well.

### Constraints handed to other tickets

- ***install.sh, written and read***: the new binary must be written to a temp path and `rename()`d into place — writing over a running executable gets `ETXTBSY`. Also: `systemctl daemon-reload` after writing the unit, `systemctl enable --now aos` on install, and refuse when `/run/systemd/system` is absent.
- ***Mode switching and the single binary***: the drain guard covers `aos daemon restart`, not just a Mode switch.
- ***Which decisions become ADRs***: the hardening-directive argument (decision 4) is ADR-shaped — it is a considered rejection that a future reader will otherwise re-litigate. So is the root-only socket.
- ***The glossary after the collapse***: `aos daemon` versus `aos service <name>` is exactly the distinction the glossary must make plain, now that Machine and Host have collapsed.
