# Spike: `install.sh`

**Throwaway.** Written for [11-install-script.md](../../../.scratch/native-linux-release/issues/11-install-script.md)
so the installer's shape could be argued over concretely. The shipped script is
written during M6 implementation; this one exists to be read and shot at.

## Run it

```sh
DRY_RUN=1 sh tools/spikes/install/install.sh    # safe anywhere, including macOS
sh tools/spikes/install/uninstall-sketch.sh --purge
```

Every mutating step goes through `run()`, and every preflight refusal through
`fail()`, so `DRY_RUN=1` prints the complete sequence of changes — including the
checks it *would* have refused on — without touching the machine. That is what
makes it reviewable from a Mac that could never pass preflight.

## What it settles

- POSIX `sh`, `set -eu`, body in `main()` called on the last line, so a
  truncated `curl | sh` defines functions and does nothing.
- Preflight order, and which failures refuse versus warn.
- Version-less asset names, so no GitHub API call and no per-IP rate limit.
- `grep`-then-`sha256sum -c`, never `--ignore-missing`, which passes vacuously.
- `install` + `mv` rather than writing over a running binary (`ETXTBSY`).
- **No `--uid 1000`**: that uid belongs to the admin's own account on stock
  Ubuntu. `aosd` looks the user up by name (`daemon_linux.go:245`).
- The commented `config.yml` template, which is also the product's first
  documentation surface.
- The unit file, including the comment block arguing why the systemd hardening
  directives are absent.
- An upgrade refuses while a Task is running, unless `FORCE=1`.

## What it deliberately does not do

Installs no packages. The Docker image's `machine` stage curates ~25 apt
packages plus a pinned Node; a native installer must not, because it is
mutating someone's real server. Agents get what the box has, and
`install_package` adds the rest on demand through the Install Ledger.

Writes no `/etc/apt/apt.conf.d/10-aos-cache`. That file redirects apt's cache
globally for Replay, and Replay is off natively.

Never runs `userdel ubuntu`. The image does; on a VPS that is the admin.
