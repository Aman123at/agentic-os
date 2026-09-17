# aosd supervises Services instead of systemd

A Docker container has no systemd, so `systemctl start nginx` fails and anything started in a shell dies with the container. `aosd` therefore acts as the Machine's service manager: a Service is a named long-running program with its command, user, and restart policy, which `aosd` starts, restarts, logs, and brings back after the Machine restarts. Programs listening on ports are reached from the Host through `aosd`'s forwarding (`<port>.localhost` subdomains, `/port/<n>/` paths for Safari, or optionally published ports) rather than by editing the compose file.

## Considered Options

- **Real systemd as PID 1**: rejected; needs a privileged container and cgroup mounts, and is fragile on Docker Desktop.
- **No Services, background shells only**: rejected; "install nginx and start it" would silently stop working after a restart.

## M6 amendment (native install, 2026-09-17)

"A Docker container has no systemd" is the **Compose install's** reason. On a
**native install** (ADR-0009) systemd is present — but it runs **the Daemon**
(`aos.service`, `Type=notify`), not the Services. `aosd` still supervises the
Services an Agent creates, because a Service is the *user's* supervised program:
it is recorded in the Install Ledger, Checkpointed, brought back on the Daemon's
own restart, and reached through `aosd`'s forwarding — none of which a bare
`systemctl` unit would give. So the decision stands, and the glossary keeps
**Daemon** (a systemd unit, `aosd`) distinct from **Service** (not a systemd unit).
Forwarding's canonical form is now `/port/<n>/` (authenticated); `<port>.localhost`
is a local convenience that cannot resolve from a remote browser. See M6.2 and §12.
