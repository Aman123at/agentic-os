# The systemd unit and the `aos service` lifecycle

Type: grilling
Status: open
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
