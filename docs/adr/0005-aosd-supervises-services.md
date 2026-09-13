# aosd supervises Services instead of systemd

A Docker container has no systemd, so `systemctl start nginx` fails and anything started in a shell dies with the container. `aosd` therefore acts as the Machine's service manager: a Service is a named long-running program with its command, user, and restart policy, which `aosd` starts, restarts, logs, and brings back after the Machine restarts. Programs listening on ports are reached from the Host through `aosd`'s forwarding (`<port>.localhost` subdomains, `/port/<n>/` paths for Safari, or optionally published ports) rather than by editing the compose file.

## Considered Options

- **Real systemd as PID 1**: rejected; needs a privileged container and cgroup mounts, and is fragile on Docker Desktop.
- **No Services, background shells only**: rejected; "install nginx and start it" would silently stop working after a restart.
