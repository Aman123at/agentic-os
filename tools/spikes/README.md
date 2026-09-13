# M0 spikes

Throwaway probes behind `docs/m0-findings.md`. Nothing here ships in the images.

| Spike | How to run (from the repo root, `cli` image built) |
|---|---|
| `sandbox-run` | `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o <dir>/sandbox-run ./tools/spikes/sandbox-run` — runs a command under a Ruleset planned from flags |
| `landlock-home/option-b.sh` | copy `sandbox-run` and the script into `<dir>`, then `docker run --rm -v <dir>:/t:ro -v <shared>:/shared --entrypoint /t/option-b.sh agentic-os:cli` |
| `replay/` | `docker run --rm -v aos-m03-pkgcache:/var/cache/aos -v $PWD/tools/spikes/replay:/r:ro --entrypoint /r/install.sh agentic-os:cli`, then the same with `--network none` and `/r/replay.sh lists` or `/r/replay.sh debs` |

On a macOS Host, keep `<dir>` and `<shared>` outside `~/Desktop`, `~/Documents` and `~/Downloads` unless Docker Desktop has been granted access to them (finding F6).
