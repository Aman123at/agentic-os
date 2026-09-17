# Release engineering: multi-arch binaries and GitHub Releases

Type: research
Status: resolved
Blocked by: —

## Question

`install.sh` will fetch a prebuilt tarball from a GitHub Release. This repo has **no git remote and no tags** today, and CI is a single Go entry point (`go run ./tools/ci`) with no GitHub Actions workflow.

Find out and write up:

1. **GoReleaser versus a hand-written workflow** for this case: one Go binary, `CGO_ENABLED=0`, `linux/amd64` and `linux/arm64`, with an embedded Vite build that must run first. Which is less machinery, and what does the GoReleaser config look like when a Node build has to happen before the Go build?
2. **How the Desktop bundle gets into the binary in CI** — `internal/webui/dist/` is gitignored and populated by the Docker build today.
3. **Artifact naming and layout** that an `install.sh` can resolve from a version string, plus how to reliably ask GitHub for "the latest release" (the `/releases/latest` API, rate limits for unauthenticated `curl`, and what to do when rate-limited).
4. **Checksums and verification.** `SHA256SUMS` as a release asset; whether signing (cosign, minisign, GPG) is worth it here and what the verification step costs a user with no tooling installed.
5. **Version stamping.** `internal/daemon/daemon_linux.go` has a `Version` constant checked by a test; reconcile that with a tag-derived `-ldflags -X` version.
6. **Publishing the Compose image to Docker Hub** from the same workflow, multi-arch, and how the tag relates to the binary release.
7. **Release tagging conventions** and whether pre-releases should be skipped by `install.sh`.

Write findings to `.scratch/native-linux-release/research/release-engineering.md` with source URLs and access dates. Do not edit any file outside that directory.

## Answer

Resolved 2026-09-17 by research. Full findings, sources and eight marked gaps: [research/release-engineering.md](../research/release-engineering.md).

**Recommendation: a hand-written workflow, not GoReleaser** — an optional `release` stage in `tools/ci/main.go`, published with `gh release create`, with the tarball binaries pulled out of the existing `aosd-ui` Dockerfile stage via `docker buildx build --output type=local`.

What decides it is *not* the Node-before-Go problem — GoReleaser handles that in a few `before: hooks:` lines, so it discriminates nothing. It is that GoReleaser's Docker support assumes it is packaging prebuilt binaries, while `docker/Dockerfile` is a 200-line build that compiles Go itself, fetches Node per-architecture and curates an Ubuntu base. GoReleaser would need a second Dockerfile that drifts from the first. Revisit the moment a `.deb`, `.rpm` or Homebrew target appears; the switching cost is about 100 lines either way, and a full `.goreleaser.yaml` sketch is in the research file so the comparison stays concrete.

### Three findings that change the plan

**1. `-ldflags -X` cannot write `Version`, and fails silently.** `internal/daemon/daemon_linux.go:67` declares it inside a `const (…)` block — verified. The linker sets string *variables*; against a const it emits no error and no warning, and the released binary reports `0.1.0-m4` forever. It must become a `var`, the symbol path is `github.com/amantiwari/agentic-os/internal/daemon.Version` (not GoReleaser's default `main.version`), and the release needs a post-build assertion because the failure is invisible.

   Checked further while verifying: `version_linux_test.go` derives the expected value from the newest `### M<n>` heading in `docs/PLAN.md`, minus one. Two consequences. Adding `### M6` in *Write M6 into docs/PLAN.md* makes that test demand `0.1.0-m5` and **fail until `Version` is bumped** — expect it, it is not a regression. And turning the const into a `var` does *not* break the test, since `go test` sees the compile-time default and only the release overrides it. The milestone-derived scheme still has to be reconciled with tag-derived versioning.

**2. Version-less asset names remove the GitHub API from `install.sh` entirely.** `/releases/latest/download/<asset>` resolves only when the asset name carries no version. Ten such requests were measured consuming **zero** of the 60/hour anonymous API budget, with no `x-ratelimit-*` headers at all. This matters because that budget is per *IP*: a NATed VPS can arrive with it already spent by strangers. The full rate-limit fallback ladder is still specified — detect on 403/429 **plus** `remaining: 0`, honour `retry-after` for secondary limits, never `curl --retry`.

**3. The first tag must be `v0.1.0`, not `v0.1.0-m6`.** `0.1.0-m4` is itself a SemVer prerelease, so a `-m6` tag makes GitHub classify the release as a prerelease, `/releases/latest` 404s, and `install.sh` fails on a brand-new project. This is why milestone-versus-release versioning cannot be deferred past the first release.

Also: prerelease-skipping in `install.sh` needs no code — `/releases/latest` excludes prereleases and drafts by definition — but `--latest` should be passed explicitly on every stable release, because "latest" sorts by `created_at`, which GitHub defines as the *commit* date.

**Signing**: `SHA256SUMS` mandatory (coreutils is everywhere, so it costs the user nothing); GitHub artifact attestations as optional hardening (`gh attestation verify`, ~6 workflow lines, no key custody); cosign redundant; minisign and GPG skipped. `install.sh` must require none of them or it breaks the "only curl and tar" promise. Note `sha256sum --ignore-missing -c` is the wrong form — it can succeed vacuously; the research file gives a grep-then-verify replacement.

**Blocked on a decision, not on more research**: the project's names disagree — `go.mod` says `github.com/amantiwari/agentic-os`, the target repo is `Aman123at/agent-os`, the binary is `aos`. The workflow hardcodes all of them. Correctly not guessed; charted as *Naming: module path, repo, binary and Docker Hub namespace*.

## Input from *Installing Chromium lazily, and Compose parity* (resolved 2026-09-17)

`tools/ci` loses the `ui+browser` image entirely (`main.go:26,38`) and the browser-presence check at `main.go:372` flips from "only the `INCLUDE_BROWSER` image has it" to "no image has it". Two stages replace it: a **pin-drift check** comparing the Chrome version pinned in Go source against `playwright-core/browsers.json`'s `chromium-headless-shell.browserVersion` — cheap, every run — and a **browser install rehearsal** inside a container, which downloads about 120 MB and therefore runs on demand rather than on every PR. The release tarball is unaffected: it never carried Chromium.

## Input from *The documentation site* (resolved 2026-09-17)

**The repository must be public before the first tag, and nobody had booked it.** Measured while writing the install page:

```
raw=404   https://raw.githubusercontent.com/Aman123at/agentic-os/main/README.md
rel=404   https://github.com/Aman123at/agentic-os/releases/latest/download/agentic-os-linux-amd64.tar.gz
```

Private release assets are not served anonymously at all — this is not the rate limit that finding 2 designed around, and no fallback ladder helps. So the version-less asset scheme, the `gh release create` publish and `install.sh`'s whole download path are correct and inert until the repository's visibility changes. M6 carries it as an ordered sub-task ahead of the release stage.

Second consumer: the documentation site deploys **on tag**, not on every push to `main`, so it can never describe a command that is not in anyone's binary. Whatever runs the release stage builds `docs-site/` too.
