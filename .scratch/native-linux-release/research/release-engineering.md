# Release engineering: multi-arch binaries and GitHub Releases

Research for `.scratch/native-linux-release/issues/09-release-engineering.md`.
Written 2026-09-17. Every external claim carries a source URL and an access date.
Empirical HTTP checks in this document were run from this Mac on 2026-09-16T18:40Z
(= 2026-09-17 local) against `goreleaser/goreleaser`, because `Aman123at/agent-os`
does not exist yet.

**Nothing here is a decision.** It is the evidence M6 needs in order to make one.

---

## 0. The repo as it actually stands

Read-only, from the working tree on 2026-09-17:

| Fact | Where |
| --- | --- |
| Module path is `github.com/amantiwari/agentic-os`, Go `1.27.1` | `go.mod:1,3` |
| SQLite is `modernc.org/sqlite` — pure Go, so `CGO_ENABLED=0` cross-compiles cleanly | `go.mod` require block |
| Desktop assets embed behind a build tag: `//go:build desktop` + `//go:embed all:dist` | `internal/webui/embed_desktop.go` |
| Without the tag, `Assets()` returns `nil` | `internal/webui/embed_none.go` |
| `internal/webui/dist/*` is gitignored, `!internal/webui/dist/.keep` is not | `.gitignore` |
| The Vite build is copied in only inside Docker: `COPY --from=desktop-build /src/desktop/dist/ internal/webui/dist/` | `docker/Dockerfile`, stage `aosd-ui` |
| Release-shaped build line already exists: `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags desktop -ldflags "-s -w" -o /out/ ./cmd/aosd` | `docker/Dockerfile`, stage `aosd-ui` |
| The Go stages are `FROM --platform=$BUILDPLATFORM`, so arm64 binaries cross-compile natively — no QEMU | `docker/Dockerfile`, stage `go-base` |
| CI is one Go entry point with named stages: `lint unit unit-linux ui image integration e2e playwright` + optional `live` | `tools/ci/main.go` |
| `tools/ci`'s `ui` stage runs `npm ci` + `npm run build` and enforces a 150 KB gzipped initial-bundle budget — but never copies `desktop/dist` into `internal/webui/dist` | `tools/ci/main.go`, `ui()` / `bundleSizes()` |
| `tools/ci`'s `unit-linux` stage already extracts files out of a Docker stage with `--output type=local,dest=…` | `tools/ci/main.go`, `unitLinux()` |
| `Version = "0.1.0-m4"` is a **`const`**, inside a `const (…)` block next to `Port`, `StateDir`, … | `internal/daemon/daemon_linux.go:67` |
| `Version` is served to the Desktop in `InfoResponse.Version` | `internal/daemon/daemon_linux.go:566` |
| A test asserts `Version == "0.1.0-m" + (highest ### M<n> heading in docs/PLAN.md) - 1` | `internal/daemon/version_linux_test.go` |
| No git remote, no tags, no `.github/` | verified with `git remote -v`, `git tag`, `ls .github` |

Two structural facts drive most of what follows:

1. **`internal/webui/dist/` is a build product that only the Dockerfile knows how to
   produce.** Any release path that is not the Dockerfile has to reimplement that copy.
2. **`Version` is a `const`, and `-ldflags -X` cannot write to a `const`.** See §6 — this
   is the single most load-bearing finding in this document, because it fails *silently*.

---

## 1. Recommendation

> **Write the workflow by hand. Drive the build from a new `release` stage in
> `tools/ci/main.go`, publish with `gh release create`, and build the tarballs by
> pulling binaries out of the existing `aosd-ui` Dockerfile stage with
> `docker buildx build --output type=local`.**
>
> Revisit GoReleaser the moment a second *packaging format* appears (`.deb`, `.rpm`,
> `.apk`, a Homebrew tap, Scoop, macOS/Windows targets). For two Linux tarballs it is
> the heavier option, not the lighter one.

### Why, specifically

The usual argument for GoReleaser is "it does the fiddly parts for free". Here the
fiddly parts are small and the fit is poor in two places.

**The Node-before-Go problem is *not* the discriminator.** GoReleaser handles it in four
lines of global `before: hooks:`, which run before anything else and abort the release if
any of them fails ([GoReleaser: Global Hooks](https://goreleaser.com/customization/hooks/),
accessed 2026-09-17). So this question, which the ticket leads with, turns out not to
decide anything. Both options handle it.

**The Docker image is the discriminator.** GoReleaser's Docker support — both the classic
`dockers` + `docker_manifests` pair and the newer `dockers_v2` (GoReleaser v2.12+, OSS, not
Pro) — is built around *reusing already-compiled binaries*: "`dockers_v2` … leverages
`docker buildx` to construct multi-architecture container manifests by reusing previously
compiled binaries and/or packages"
([GoReleaser: Docker v2](https://goreleaser.com/customization/dockers_v2/), accessed
2026-09-17). `docker/Dockerfile` is not that shape. It is a ~200-line multi-stage build that
compiles Go itself, downloads and SHA-verifies a Node tarball per architecture, optionally
downloads Chromium's headless shell, `apt-get install`s a curated Ubuntu base, creates the
`aos` user, and runs an `ldd` guard. To put GoReleaser in front of that you would either
maintain a second, binary-consuming Dockerfile (two image definitions that can drift), or
bypass GoReleaser's Docker pipe entirely — at which point you are hand-writing the image
half anyway and GoReleaser is only doing tar + sha256 + `gh release create`.

**The repo has one deliberate CI seam and GoReleaser adds a second.** `tools/ci/main.go`
opens with "It works the same locally and in GitHub Actions." That is a real property worth
keeping: `go run ./tools/ci release` should build the same tarballs on Aman's Mac as on a
runner. GoReleaser introduces a second configuration language (Go-template-in-YAML), a
second tool to pin and install, and a second place where "how we build" is written down.

**What you give up by not using GoReleaser** (stated fairly, because the balance is closer
than the recommendation makes it sound):

- Default `-s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}} -X main.builtBy=goreleaser`
  ldflags ([GoReleaser: Go builder](https://goreleaser.com/customization/builds/go/),
  accessed 2026-09-17). Useless here as-is — the variable is `internal/daemon.Version`, not
  `main.version` — so it would need overriding anyway.
- `prerelease: auto`, which "will mark the release as not ready for production in case there
  is an indicator for this in the tag e.g. v1.0.0-rc1"
  ([GoReleaser: Release](https://goreleaser.com/customization/release/), accessed
  2026-09-17). Replaced by one `case` statement — see §3.
- Checksum file generation, archive naming, changelog generation, snapshot builds.
- A ready-made on-ramp to nfpm (`.deb`/`.rpm`), Homebrew and SBOMs **later**.

That last bullet is the honest counter-case. If M6 or M7 decides to ship a `.deb` — which
is plausible for an "Ubuntu VPS" product — GoReleaser stops being overhead and starts being
the cheap path. The switching cost is low in either direction (~100 lines), so this is not a
one-way door. A GoReleaser configuration sketch is in §8 so the comparison stays concrete.

---

## 2. Getting the Desktop bundle into the binary in CI

The release build must do three things in order:

1. `npm ci --prefix desktop && npm --prefix desktop run build` → `desktop/dist/`
2. copy `desktop/dist/` → `internal/webui/dist/` (gitignored, so it starts empty)
3. `go build -tags desktop` — without the tag the embed file is excluded and the binary
   ships no Desktop at all, with no error.

Note step 3's failure mode: **omitting `-tags desktop` produces a perfectly good binary that
silently has no UI**, because `embed_none.go` provides a `nil`-returning `Assets()`. Whatever
path is chosen needs a post-build assertion (binary size, or a `strings`/embed probe, or
simply starting it and fetching `/`).

### Option A (recommended): reuse the Dockerfile stage

```sh
docker buildx build -f docker/Dockerfile --target aosd-ui \
  --platform linux/amd64,linux/arm64 \
  --build-arg AOS_VERSION="$TAG" \
  --output type=local,dest=out .
# -> out/linux_amd64/aosd, out/linux_arm64/aosd
```

Why this is the strongest option:

- **Zero drift.** The tarball binary and the image binary come from the same stage, the same
  Go toolchain pin (`ARG GO_VERSION=1.27.1`), the same Node pin, the same flags. A release
  cannot ship a binary that differs from the one the Compose image ships.
- **No QEMU.** `go-base` and `desktop-build` are `FROM --platform=$BUILDPLATFORM`; the arm64
  binary is produced by Go cross-compilation (`GOOS=$TARGETOS GOARCH=$TARGETARCH`), which is
  safe precisely because `CGO_ENABLED=0` and `modernc.org/sqlite` is pure Go.
- **The pattern is already proven in this repo.** `tools/ci`'s `unitLinux()` uses
  `--output type=local,dest=` against the `go-test` stage, with a comment recording *why*:
  "The repository goes in as build context, never a bind mount, which hangs Docker Desktop
  for folders under ~/Desktop." `--output type=local` is an export, not a bind mount, so the
  known Docker Desktop hang on this Mac does not apply.

One change is required: `aosd-ui` hardcodes `-ldflags "-s -w"`. It needs an
`ARG AOS_VERSION` threaded into the `-X` flag (see §6). Because the stage is shared with the
`ui` image target, doing this once fixes version stamping for both artifacts at the same time.

### Option B: build natively on the runner

```sh
npm ci --prefix desktop && npm --prefix desktop run build
rm -rf internal/webui/dist && mkdir -p internal/webui/dist && touch internal/webui/dist/.keep
cp -R desktop/dist/. internal/webui/dist/
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
    go build -trimpath -tags desktop -ldflags "-s -w -X ${MOD}/internal/daemon.Version=${VER}" \
    -o "dist/linux_${arch}/aosd" ./cmd/aosd
done
```

Faster (no Docker in the loop) and it reuses `tools/ci`'s existing `ui()` stage, which also
enforces the bundle budget on the very artifact being shipped — a genuine bonus. The cost is
that the "copy dist into webui" rule now lives in two places (here and the Dockerfile), and
they can drift. Note the `rm -rf` must not take `.keep` with it, or `//go:embed all:dist`
fails to find a directory on a clean checkout where nothing was built.

**Gap:** I could not confirm from a primary source that GitHub-hosted runners provide a Go
1.27 toolchain. `actions/setup-go` supports `go-version-file: go.mod` and "Supports both `go`
and `toolchain` directives in `go.mod`. If the `toolchain` directive is present, its version
is used; otherwise, the action falls back to the `go` directive"
([actions/setup-go README](https://github.com/actions/setup-go), accessed 2026-09-17), and Go's
own toolchain mechanism can fetch a newer toolchain on demand — but whether that resolves
cleanly for `1.27.1` on a runner in September 2026 should be checked empirically on the first
workflow run rather than assumed. Option A sidesteps this entirely by using
`golang:1.27.1-bookworm` from the Dockerfile.

---

## 3. Artifact naming, layout, and resolving "latest"

### The naming decision is really a rate-limit decision

GitHub documents a permalink for the newest release's assets: "To link directly to a download
of your latest release asset that was manually uploaded, the suffix is
`/releases/latest/download/asset-name.zip`"
([GitHub Docs: Linking to releases](https://docs.github.com/en/repositories/releasing-projects-on-github/linking-to-releases),
accessed 2026-09-17).

That permalink **only works if the asset name does not contain the version**. Which means the
naming choice determines whether `install.sh` needs the GitHub API at all.

**Recommended asset names (version-less):**

```
aos_linux_amd64.tar.gz
aos_linux_arm64.tar.gz
SHA256SUMS
```

and the whole of "resolve latest" collapses to one line with zero API calls:

```sh
curl -fsSL "https://github.com/Aman123at/agent-os/releases/latest/download/aos_linux_${arch}.tar.gz" -o "$tmp/aos.tar.gz"
```

Costs of version-less names, stated plainly:

- The filename no longer says which version it is. Two downloads collide in `~/Downloads`.
  Mitigation: `aosd --version` after install is authoritative, and each release's asset list
  is already scoped to its own tag page.
- It diverges from the GoReleaser default, whose archive `name_template` is
  `"{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}…"`
  ([GoReleaser: Archives](https://goreleaser.com/customization/archive/), accessed
  2026-09-17). Under GoReleaser you would override both this and the checksum template
  (default `"{{ .ProjectName }}_{{ .Version }}_checksums.txt"` —
  [GoReleaser: Checksum](https://goreleaser.com/customization/checksum/), accessed
  2026-09-17). Doable, just deliberate.

If versioned names are preferred anyway, `install.sh` must first learn the tag — see the
three-tier ladder below.

### Tarball layout

Keep it flat, one level, no wrapper directory (GoReleaser's `wrap_in_directory` defaults to
`false` anyway). Proposal:

```
aosd                     # the binary, mode 0755
LICENSE
README.md
systemd/aos.service      # the unit install.sh installs
config.example.yml       # commented template for /etc/aos/config.yml
```

`install.sh` then does `tar -xzf … -C "$tmp"` and `install -m 0755 "$tmp/aosd"
/usr/local/bin/aosd`, then `ln -sf aosd /usr/local/bin/aos` — matching how the image already
links `aos` → `aosd` (`docker/Dockerfile`, `machine` stage). Do **not** ship the symlink
inside the tarball; a symlink in a tar extracted as root is an unnecessary sharp edge, and
`ln -sf` is one line.

Ship `systemd/aos.service` and `config.example.yml` *in the tarball*, not fetched separately:
it keeps the "only curl and tar" promise and makes the unit file version-matched to the binary.

### Resolving "latest": a three-tier ladder

**Tier 1 — no API at all (recommended default).** As above, hit
`/releases/latest/download/<name>` directly. Verified 2026-09-16T18:40Z: that URL 302s
through to a signed `release-assets.githubusercontent.com` URL and returns 200.

**Tier 2 — learn the tag without the API.** The *web* URL `/releases/latest` is a plain 302:

```
$ curl -sSI 'https://github.com/goreleaser/goreleaser/releases/latest'
HTTP/2 302
location: https://github.com/goreleaser/goreleaser/releases/tag/v2.18.1
```

(verified 2026-09-16T18:40Z). So:

```sh
tag=$(curl -fsSI "https://github.com/$REPO/releases/latest" \
      | sed -n 's/^[Ll]ocation:.*\/tag\/\(.*\)$/\1/p' | tr -d '\r')
```

or, avoiding header parsing, `curl -fsSL -o /dev/null -w '%{url_effective}'` and take the
basename. This is documented as a link target, not as an API contract — treat the exact
redirect shape as observed behaviour, not a promise, and always have the failure branch print
something actionable.

**Tier 3 — the REST API, only when you need metadata.**
`GET /repos/{owner}/{repo}/releases/latest` returns "The most recent non-prerelease,
non-draft release, sorted by the `created_at` attribute"
([GitHub REST: Releases](https://docs.github.com/en/rest/releases/releases?apiVersion=2022-11-28),
accessed 2026-09-17). Use it only if `install.sh` needs the release body, asset list or
publish date — which, for a script that only shells out to `curl` and `tar`, it does not.

### Unauthenticated rate limits, and what to do when you hit them

- "The primary rate limit for unauthenticated requests is 60 requests per hour", and these
  are "associated with the originating IP address, not with the user or application that made
  the request"
  ([GitHub REST: Rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2022-11-28),
  accessed 2026-09-17).
- A PAT gets 5,000/hour; `GITHUB_TOKEN` inside Actions gets 1,000/hour per repository
  (same source).
- Exceeding it returns "a `403` or `429` response", with `x-ratelimit-remaining`,
  `x-ratelimit-reset` (UTC epoch seconds) and, for *secondary* limits, `retry-after`
  (same source).

**60/hour per IP is the danger.** A VPS behind carrier NAT, a shared CI egress IP, or a
corporate NAT can have that budget already spent by strangers before `install.sh` ever runs.
This is the strongest single argument for Tier 1.

**Empirically confirmed (2026-09-16T18:40Z):** the web path does not touch that budget.
Ten successive `HEAD https://github.com/…/releases/latest` requests left
`api.github.com/rate_limit` reporting an unchanged `"remaining": 57, "used": 3`, and the 302
response carries no `x-ratelimit-*` headers at all. Asset downloads likewise redirect to
`release-assets.githubusercontent.com` and carry no rate-limit headers. GitHub separately
states of releases: "There is no limit on the total size of a release, nor bandwidth usage"
([GitHub Docs: About releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases),
accessed 2026-09-17).

**If `install.sh` does end up calling the API, the rate-limited branch should:**

1. Detect it properly — `403`/`429` **plus** `x-ratelimit-remaining: 0`. A bare 403 with
   remaining > 0 is something else (private repo, bad token) and deserves a different message.
2. Distinguish the secondary limit: if `retry-after` is present, honour it.
3. Fall back, not fail: drop to Tier 2, then Tier 1.
4. Only if all of that fails, print something a human can act on:

```
GitHub's anonymous API limit (60 requests/hour per IP) is exhausted for this address.
It resets at 2026-09-17 14:22 UTC.

Either wait, or install a specific version without the API:
  curl -fsSL https://…/install.sh | sh -s -- --version v0.1.0
Or supply a token:
  GITHUB_TOKEN=ghp_… curl -fsSL https://…/install.sh | sh
```

Turn `x-ratelimit-reset` into that timestamp with `date -u -d "@$reset"` (GNU date; on a
BusyBox/Alpine host use `date -u -d "@$reset"` too, but do not assume `-d @` works
everywhere — print the raw epoch as a fallback rather than letting `date` fail under `set -e`).

5. **Do not retry with `curl --retry`.** Retrying a 403 rate limit does not help and pushes
   you toward the secondary limit.

Also: **always pass a `User-Agent`.** GitHub's API rejects requests without one, and a
descriptive UA (`aos-install/0.1`) makes any future abuse report legible.

---

## 4. Checksums and signing

### `SHA256SUMS` — yes, unconditionally

One file per release, both architectures, standard GNU coreutils format
(`<64 hex>` + two spaces + filename). `sha256sum` ships in `coreutils` on every Ubuntu and
Debian base, so the cost to the user is literally zero.

The obvious verification line is:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

`--ignore-missing` means "don't fail or report status for missing files"
([man7: sha256sum(1)](https://man7.org/linux/man-pages/man1/sha256sum.1.html), accessed
2026-09-17), which is what you want when the release lists both arches and you downloaded one.

**But do not use that form.** `--ignore-missing` degrades badly when *nothing* matches — a
renamed asset or a truncated `SHA256SUMS` can turn "verified nothing" into something a
careless `set -e` treats as success. **Gap: I did not verify coreutils' exact exit status when
zero listed files are present.** Sidestep it with a form that has no such edge:

```sh
expected=$(grep -E "  ${file}\$" SHA256SUMS | cut -d' ' -f1)
[ -n "$expected" ] || die "no checksum recorded for $file"
printf '%s  %s\n' "$expected" "$file" | sha256sum -c -
```

This fails loudly on a missing line, a renamed asset, and a corrupt download alike.

**Be honest about what this buys.** `SHA256SUMS` is fetched over the same TLS connection to
the same host as the tarball. It detects truncation, a bad CDN edge and disk corruption. It
does **not** detect a compromised GitHub account or a malicious release, because an attacker
who can replace the tarball can replace the sums file. Say so in the docs rather than letting
"checksum verified" imply more than it is.

### Signing: ranked by cost to a user who has nothing installed

| Option | What the user runs | What they must install | Verdict |
| --- | --- | --- | --- |
| **GitHub artifact attestations** | `gh attestation verify aos_linux_amd64.tar.gz -R Aman123at/agent-os` | `gh` (apt repo or a ~15 MB tarball); needs network for the trust bundle | **Do this.** ~6 lines of workflow, no key custody |
| **cosign keyless (Sigstore)** | `cosign verify-blob <file> --bundle artifact.sigstore.json --certificate-identity=… --certificate-oidc-issuer=…` | `cosign` binary | Redundant with attestations; the identity string is long and easy to get wrong |
| **minisign** | `minisign -Vm aos_linux_amd64.tar.gz -P RW…` | `minisign` (Ubuntu universe, or build with cmake/libsodium/zig) | **Skip.** Requires holding a long-lived Ed25519 private key as a repo secret |
| **GPG** | `gpg --verify aos.tar.gz.asc` after importing a key from a keyserver | `gnupg` + the whole key-distribution problem | **Skip.** Highest friction, lowest actual verification rate |

Sources: attestation permissions are `id-token: write`, `contents: read`,
`attestations: write`, and consumers verify with
`gh attestation verify PATH/TO/ARTIFACT -R ORG/REPO`
([GitHub Docs: Using artifact attestations](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations),
accessed 2026-09-17). Cosign's blob form is
`cosign verify-blob <file> --bundle artifact.sigstore.json --certificate-identity=name@example.com --certificate-oidc-issuer=https://accounts.example.com`
([Sigstore: Verifying](https://docs.sigstore.dev/cosign/verifying/verify/), accessed
2026-09-17). Minisign verification is `minisign -Vm myfile.txt -P RW…` with the `.minisig`
beside the file, and install is "compile from source" or a prebuilt binary
([minisign](https://jedisct1.github.io/minisign/), accessed 2026-09-17).

**The important design rule: `install.sh` must not require any of them.** The whole premise is
"only `curl` and `tar`" on a fresh VPS. So:

- `install.sh` verifies `SHA256SUMS` — mandatory, free.
- Attestations exist for anyone who cares, documented on the docs site as an optional
  hardening step, never as a step in the happy path.
- `--skip-checksum` should not exist as a flag. There is no legitimate reason for it, and it
  is the flag people paste from a forum post.

---

## 5. Publishing the image to Docker Hub

### The workflow half

Docker's own documented multi-platform Actions recipe
([Docker Docs: Multi-platform image with GitHub Actions](https://docs.docker.com/build/ci/github-actions/multi-platform/),
accessed 2026-09-17):

```yaml
- uses: docker/login-action@v4
  with:
    username: ${{ vars.DOCKERHUB_USERNAME }}
    password: ${{ secrets.DOCKERHUB_TOKEN }}
- uses: docker/setup-qemu-action@v4
- uses: docker/setup-buildx-action@v4
- uses: docker/build-push-action@v7
  with:
    platforms: linux/amd64,linux/arm64
    push: true
    tags: user/app:latest
```

Use a Docker Hub **access token**, not the account password, as `DOCKERHUB_TOKEN`.

### QEMU or a two-runner matrix?

Docker warns that "building multiple platforms on the same runner can significantly extend"
build time and recommends distributing per-platform builds across runners (same source).

For *this* Dockerfile the picture is better than usual: `desktop-build`, `go-base`,
`aosd-ui` and `node-dist` are all `FROM --platform=$BUILDPLATFORM`, so the expensive Node and
Go work runs natively on amd64 regardless of target. Only the final `machine`/`ui` stages —
an `apt-get install` of ~30 packages onto `ubuntu:24.04`, plus the optional Chromium stage —
run emulated for arm64. `apt` under QEMU is slow but not pathological.

If it does get slow, the escape hatch is free: GitHub offers `ubuntu-24.04-arm` /
`ubuntu-22.04-arm` labels, and for public repositories these are free and unlimited at 4 vCPU
/ 16 GB
([GitHub Docs: GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners),
accessed 2026-09-17). Note there is **no** `ubuntu-latest-arm` label — the version must be
pinned. The pattern is: matrix over `ubuntu-latest` + `ubuntu-24.04-arm`, each pushing by
digest, then one job assembling the manifest.

**Start with QEMU on a single runner. Move to the matrix if the arm64 leg passes ~15 minutes.**

### How image tags relate to the binary release

Same tag push, same source commit, three image tags:

| Git tag | Image tags pushed |
| --- | --- |
| `v0.1.0` | `:0.1.0`, `:0.1`, `:latest` |
| `v0.1.1` | `:0.1.1`, `:0.1`, `:latest` |
| `v0.2.0-rc.1` | `:0.2.0-rc.1` **only** — never `:latest`, never `:0.2` |

Rules that matter:

- **A prerelease must never move `:latest`.** Same predicate as the GitHub release's
  `--prerelease` flag, so derive both from one place.
- **`compose.yaml` should pin an explicit tag**, not `:latest`, so an existing Compose user's
  `docker compose pull` is deterministic and an upgrade is a visible diff.
- **Re-tagging needs no rebuild.** `docker buildx imagetools create -t repo/app:latest
  repo/app:v0.1.0` — "If only one source is specified and that source is a manifest list or
  image index, create performs a carbon copy"
  ([Docker Docs: buildx imagetools create](https://docs.docker.com/reference/cli/docker/buildx/imagetools/create/),
  accessed 2026-09-17). Useful for promoting an rc to stable without another 20-minute build.
- The image and the tarball must carry the **same** `AOS_VERSION` build arg, or
  `aosd --version` will disagree between the two install shapes and every bug report gets
  harder.

**Worth flagging to the docs:** anonymous Docker Hub pulls are limited to "100 per IPv4
address or IPv6 /64 subnet" per 6 hours, 200 for a free authenticated account
([Docker Docs: Pull usage and limits](https://docs.docker.com/docker-hub/usage/pulls/),
accessed 2026-09-17). Another quiet argument for the native tarball being the primary path.

**Gap — a naming inconsistency somebody has to resolve:** `go.mod` says
`github.com/amantiwari/agentic-os`; the release target is `Aman123at/agent-os`. Three
different spellings of the project (`agentic-os`, `agent-os`, `aos`) are now in play, and the
Docker Hub namespace is undecided. GoReleaser would infer owner/name from the origin remote
([GoReleaser: Release](https://goreleaser.com/customization/release/), accessed 2026-09-17),
which would *not* match the module path. Changing the module path is a repo-wide breaking
edit and is out of scope for this ticket — but the release workflow hardcodes a repo slug and
an image namespace, so M6 has to pick. **I am not guessing which.**

---

## 6. Version stamping — the silent-failure finding

### `-X` cannot write to a `const`

From the linker's own documentation:

> `-X importpath.name=value`
> Set the value of the **string variable** in importpath named name to value.
> This is only effective if the variable is declared in the source code either uninitialized
> or initialized to a constant string expression. `-X` will not work if the initializer makes
> a function call or refers to other variables.

([pkg.go.dev: cmd/link](https://pkg.go.dev/cmd/link), accessed 2026-09-17)

`internal/daemon/daemon_linux.go:67` declares `Version = "0.1.0-m4"` inside a `const (…)`
block. **`-X` against it is a no-op, and it is a silent one** — no build error, no warning,
just a released binary that reports `0.1.0-m4` forever. This is the kind of thing that ships.

Two consequences:

1. `Version` must move out of the `const` block into a `var` block:
   `var Version = "0.1.0-m4"`. (Initialisation to a constant string expression is fine; the
   linker doc says so explicitly.)
2. The import path is `github.com/amantiwari/agentic-os/internal/daemon.Version`, **not**
   `main.version`. GoReleaser's default ldflags target `main.version` and would need
   overriding.

And because the failure is silent, the release must *prove* the stamp landed:

```sh
got=$(./dist/linux_amd64/aosd --version)          # or however the binary reports it
[ "$got" = "$EXPECTED" ] || die "ldflags did not apply: got $got, want $EXPECTED"
```

This check is cheap and catches both the `const` regression and a typo'd import path.

### Reconciling the milestone version with the tag

There are two version identities and they answer different questions:

- **`0.1.0-m4`** answers *"how far along is the plan?"*. It is asserted by
  `internal/daemon/version_linux_test.go`, which parses `docs/PLAN.md` for the highest
  `### M<n>` heading and requires `Version == "0.1.0-m" + (n-1)` — "the newest heading is the
  milestone being worked towards; the Machine reports the one that is finished, so it trails
  by one."
- **`v0.1.0`** answers *"which build am I running, and which bug report is this?"*. That is
  the question a stranger who ran `install.sh` will actually have.

Three ways to reconcile:

**(a) Keep the source default, let ldflags override it.** Minimal diff: `const` → `var`, add
`-X`. The test still passes because `go test` builds without ldflags and sees the milestone
string. Released binaries report the tag.
*Cost:* the test now covers a value no user ever sees, and `aosd --version` means different
things in a dev build and a release build. Needs the post-build assertion above, or the stamp
can rot unnoticed.

**(b) Two fields.** `Version` stays the milestone (const, test-asserted); add
`var Release = "dev"` stamped from the tag. `InfoResponse` carries both; Settings ▸ Status
shows `0.1.2 (M5)`.
*Cost:* two numbers to explain, a proto field to add, and users will still quote the wrong one.

**(c) Retire the milestone version at M6.** The tag becomes the only version. The source
default becomes `dev` (or `git describe --tags --always --dirty`). The test changes shape:
instead of "matches the plan's newest milestone" it asserts "the source default is `dev`, so
an unstamped build is visibly not a release" — which is a *better* test, because it catches
the silent-`const` bug directly.
*Cost:* loses the "the plan and the code can't drift" property that M4.8 item 8.11 was written
to fix. Mitigate by keeping a milestone marker somewhere the test can still assert
(a `docs/PLAN.md`-derived constant that is not the user-facing version).

> **Recommendation: (c) is the right M6 destination; (a) is the right transition.**
> M6's entire premise is that people who have never read `docs/PLAN.md` will install this.
> A version string that encodes internal milestone numbering is not useful to them, and
> `0.1.0-m4` is *itself* a SemVer prerelease — see the trap in §7. Ship (a) with the release
> workflow, then fold the milestone version into (c) as part of M6's own work, so the two
> changes can be reviewed apart.

### Untagged builds

CI on `main`, and local `go run ./tools/ci release`, must still produce something. Use
`git describe --tags --always --dirty` (requires `fetch-depth: 0` on checkout) or
`0.0.0-dev+<shortsha>`. The point is that a non-release build is *obviously* not a release
when it turns up in a bug report.

### Flag mechanics

`docker/Dockerfile` already passes `-trimpath -ldflags "-s -w"`. `-X` **appends inside** the
existing `-ldflags` string; it is not a second flag:

```
-ldflags "-s -w -X github.com/amantiwari/agentic-os/internal/daemon.Version=${AOS_VERSION}"
```

Keep `-trimpath` — it strips local paths from the binary and is a prerequisite for anything
resembling reproducibility later.

---

## 7. Tagging conventions and prereleases

**Convention:** annotated SemVer tags with a `v` prefix — `v0.1.0`, `v0.1.1`, `v0.2.0-rc.1`.
Annotated, so `gh release create --notes-from-tag` and `git describe` both work. The `v`
prefix is the ecosystem default and GoReleaser's `.Version` is "the version being released
with the `v` prefix stripped" ([GoReleaser: Templates](https://goreleaser.com/customization/templates/),
accessed 2026-09-17) — so `.Tag` is `v0.1.0` and `.Version` is `0.1.0`. `install.sh --version`
should accept both spellings and normalise.

### Should `install.sh` skip prereleases? — it gets this for free

`/releases/latest` (both the REST endpoint and the web redirect) returns "The most recent
non-prerelease, non-draft release"
([GitHub REST: Releases](https://docs.github.com/en/rest/releases/releases?apiVersion=2022-11-28),
accessed 2026-09-17). So an `install.sh` built on Tier 1 or Tier 2 of §3 **never sees a
prerelease at all**. Nothing to implement.

The only requirement is on the publishing side: mark prereleases as such. `gh release create
--prerelease`, or GoReleaser's `prerelease: auto`, which detects the indicator in the tag.
Opting in stays possible and explicit: `install.sh --version v0.2.0-rc.1`.

### Three traps

**1. `0.1.0-m4` is a SemVer prerelease.** If the first tag is `v0.1.0-m6` — which is the
obvious thing to do given the current version string — GitHub will classify it as a
prerelease, `/releases/latest` will return 404 because there is no non-prerelease release, and
`install.sh` will fail on a brand-new project with a confusing error. **The first real tag
must be `v0.1.0` with no suffix.** This is the concrete reason the §6 reconciliation cannot be
deferred past the first release.

**2. "Latest" is sorted by `created_at`, which is a commit date.** GitHub: "The `created_at`
attribute is the date of the commit used for the release, and not the date when the release
was drafted or published" (same source). Tag an older commit and "latest" can land somewhere
surprising. `gh release create --latest` documents its default as
"[automatic based on date and version]" and lets you force it (verified against the local
`gh release create --help`, 2026-09-17). **Pass `--latest` explicitly on every stable release**
and never tag out of chronological order. *Gap: the REST doc describes the API endpoint; I
found no primary doc stating whether the **web** `/releases/latest` redirect honours
`make_latest` identically. Setting it explicitly makes the question moot.*

**3. A tag pushed by `GITHUB_TOKEN` will not trigger the release workflow.** "When a workflow
run pushes code using the repository's GITHUB_TOKEN, a new workflow will not run even when the
repository contains a workflow configured to run when push events occur"
([GitHub Docs: GITHUB_TOKEN](https://docs.github.com/en/actions/concepts/security/github_token),
accessed 2026-09-17). So a "bump version and tag" automation would silently never release. Push
tags from a local clone (which is the right default for a one-maintainer project anyway), or
use a PAT / GitHub App token if that ever changes.

Also worth knowing: `gh release create` with assets "makes separate API calls to create the
release as a draft, upload the assets, and then publish the release", and immutability
protections apply only after publish (from `gh release create --help`, 2026-09-17). That
draft-then-publish behaviour is what stops `/releases/latest` from briefly pointing at a
release whose assets have not finished uploading.

---

## 8. Sketches

### 8a. Recommended: `.github/workflows/release.yml`

```yaml
name: release

on:
  push:
    tags: ['v*']          # https://docs.github.com/en/actions/reference/... events-that-trigger-workflows

permissions:
  contents: write         # create the release, upload assets
  id-token: write         # Sigstore OIDC, for attestations
  attestations: write

jobs:
  tarballs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }             # tags, for git describe
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - uses: docker/setup-buildx-action@v4

      # One entry point, same as every other stage (tools/ci/main.go).
      - run: go run ./tools/ci release
        env:
          AOS_VERSION: ${{ github.ref_name }}

      - uses: actions/attest-build-provenance@v3
        with:
          subject-path: 'dist/*.tar.gz'

      - name: Publish
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          set -eu
          case "${GITHUB_REF_NAME}" in
            *-*) flag=--prerelease ;;        # v0.2.0-rc.1
            *)   flag=--latest ;;
          esac
          gh release create "${GITHUB_REF_NAME}" \
            dist/aos_linux_amd64.tar.gz \
            dist/aos_linux_arm64.tar.gz \
            dist/SHA256SUMS \
            --verify-tag --generate-notes "$flag"

  image:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: docker/setup-qemu-action@v4
      - uses: docker/setup-buildx-action@v4
      - uses: docker/login-action@v4
        with:
          username: ${{ vars.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}
      - uses: docker/build-push-action@v7
        with:
          context: .
          file: docker/Dockerfile
          target: ui
          platforms: linux/amd64,linux/arm64
          push: true
          build-args: AOS_VERSION=${{ github.ref_name }}
          tags: |
            ${NAMESPACE}/agentic-os:${{ github.ref_name }}
            # :latest added by a later imagetools step only for stable tags
```

### 8b. The `release` stage in `tools/ci/main.go`

Sketch only — shape, not final code. It slots in beside the existing stages:

```go
{"release", release, true},   // optional: never part of the default sweep
```

```go
// release builds the two Linux tarballs and SHA256SUMS into dist/.
// AOS_VERSION names the release; without it, a dev version is derived from git.
func release() error {
    ver := os.Getenv("AOS_VERSION")
    if ver == "" {
        out, err := output("git", "describe", "--tags", "--always", "--dirty")
        if err != nil { return err }
        ver = "0.0.0-dev+" + strings.TrimSpace(out)
    }
    // 1. Cross-build both arches out of the Dockerfile's aosd-ui stage.
    //    Same stage the ui image uses, so tarball and image cannot drift.
    //    --output type=local is an export, not a bind mount (see unitLinux).
    if err := run("docker", "buildx", "build", "-f", "docker/Dockerfile",
        "--target", "aosd-ui", "--platform", "linux/amd64,linux/arm64",
        "--build-arg", "AOS_VERSION="+ver,
        "--output", "type=local,dest=dist/bin", "."); err != nil { return err }

    // 2. Assert the ldflags actually landed. -X against a const is a silent
    //    no-op (cmd/link docs), so this check is the only thing that catches it.
    // 3. Assert the Desktop is embedded: without -tags desktop the binary builds
    //    fine and serves nothing.
    // 4. tar -czf each arch with aosd + LICENSE + README + systemd/ + config.example.yml
    // 5. Write dist/SHA256SUMS over the tarballs.
    return nil
}
```

Keeping this in Go rather than shell preserves the "works the same locally" property: Aman can
run `go run ./tools/ci release` on the Mac and inspect the exact tarballs before a tag exists.

### 8c. For comparison: the GoReleaser configuration

If the decision goes the other way, this is roughly the whole of it:

```yaml
version: 2
project_name: aos

before:
  hooks:
    - npm ci --prefix desktop
    - npm --prefix desktop run build
    # The dist dir is gitignored; clear stale files but keep .keep for go:embed.
    - sh -c 'find internal/webui/dist -mindepth 1 ! -name .keep -delete'
    - sh -c 'cp -R desktop/dist/. internal/webui/dist/'

builds:
  - id: aosd
    main: ./cmd/aosd
    binary: aosd
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64, arm64]
    tags: [desktop]                     # without this the binary has no Desktop
    flags: [-trimpath]
    ldflags:
      - -s -w
      # NOT main.version — GoReleaser's default would silently miss.
      - -X github.com/amantiwari/agentic-os/internal/daemon.Version={{ .Version }}

archives:
  - formats: [tar.gz]
    # Version-less, so install.sh can use /releases/latest/download/<name>.
    name_template: 'aos_{{ .Os }}_{{ .Arch }}'
    files: [LICENSE, README.md, systemd/aos.service, config.example.yml]

checksum:
  name_template: 'SHA256SUMS'

release:
  github: { owner: Aman123at, name: agent-os }
  prerelease: auto                       # v0.2.0-rc.1 is marked automatically
```

Note what this does *not* cover: the Docker image. `dockers_v2` would need a second,
binary-consuming Dockerfile — see §1.

---

## 9. Open gaps, marked rather than guessed

1. **Go 1.27.1 on GitHub-hosted runners.** Not verified from a primary source. Option A in §2
   avoids the question by using the Dockerfile's pinned `golang:1.27.1-bookworm`.
2. **Repo / module / image naming.** `go.mod` says `github.com/amantiwari/agentic-os`; the
   target repo is `Aman123at/agent-os`; the CLI is `aos`; the images are `agentic-os:cli|ui`.
   The Docker Hub namespace is undecided. The workflow hardcodes all three. M6 must pick; I am
   not guessing.
3. **Whether the web `/releases/latest` redirect honours `make_latest` or strictly
   `created_at`.** Only the REST endpoint is documented. Setting `--latest` explicitly on every
   stable release makes it moot.
4. **`sha256sum --ignore-missing -c` exit status when zero listed files exist.** Not verified.
   The `grep`-then-verify form in §4 does not depend on it.
5. **Nothing here has been run against `Aman123at/agent-os`,** because no remote and no tags
   exist. The `/releases/latest` behaviours were verified against `goreleaser/goreleaser`.
6. **`docs/PLAN.md` has no M6 section yet,** so the shape of `version_linux_test.go` after M6
   (§6, option c) is a decision to be made, not a fact to be reported.
7. **Non-glibc / non-systemd targets.** `CGO_ENABLED=0` means the binary itself is
   static and would run on Alpine — but the tarball ships a systemd unit and `install.sh`
   assumes systemd. The map already lists this as unspecified; release engineering does not
   resolve it.
8. **Reproducible builds.** `-trimpath` is present; `SOURCE_DATE_EPOCH`, a pinned Node/npm
   resolution and a deterministic Vite output hash were not investigated. Out of scope for
   this ticket, but a prerequisite if anyone later wants to independently rebuild a release
   and compare checksums.

---

## Sources

All accessed 2026-09-17 unless noted.

**GoReleaser**
- Go builder / default ldflags — https://goreleaser.com/customization/builds/go/
- Global hooks — https://goreleaser.com/customization/hooks/
- Archives / `name_template` — https://goreleaser.com/customization/archive/
- Checksum — https://goreleaser.com/customization/checksum/
- Release / `prerelease: auto`, `make_latest` — https://goreleaser.com/customization/release/
- Templates / `.Version` vs `.Tag` — https://goreleaser.com/customization/templates/
- Docker v2 — https://goreleaser.com/customization/dockers_v2/
- Docker manifests — https://goreleaser.com/customization/docker_manifest/
- Snapshots — https://goreleaser.com/customization/snapshots/
- GitHub Actions recipe — https://goreleaser.com/ci/actions/

**GitHub**
- REST: releases / "latest" definition — https://docs.github.com/en/rest/releases/releases?apiVersion=2022-11-28
- REST: rate limits — https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api?apiVersion=2022-11-28
- Linking to releases (`/releases/latest/download/…`) — https://docs.github.com/en/repositories/releasing-projects-on-github/linking-to-releases
- About releases (asset size, bandwidth) — https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases
- `GITHUB_TOKEN` (recursion guard) — https://docs.github.com/en/actions/concepts/security/github_token
- Events that trigger workflows (`on: push: tags:`) — https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
- GitHub-hosted runners (`ubuntu-24.04-arm`) — https://docs.github.com/en/actions/reference/runners/github-hosted-runners
- Artifact attestations — https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations
- `actions/setup-go` (`go-version-file`) — https://github.com/actions/setup-go

**Docker**
- Multi-platform builds in GitHub Actions — https://docs.docker.com/build/ci/github-actions/multi-platform/
- `buildx imagetools create` — https://docs.docker.com/reference/cli/docker/buildx/imagetools/create/
- Docker Hub pull limits — https://docs.docker.com/docker-hub/usage/pulls/

**Go / signing / coreutils**
- `cmd/link` `-X` semantics — https://pkg.go.dev/cmd/link
- Sigstore cosign verification — https://docs.sigstore.dev/cosign/verifying/verify/
- minisign — https://jedisct1.github.io/minisign/
- `sha256sum(1)` — https://man7.org/linux/man-pages/man1/sha256sum.1.html

**Local, non-web**
- `gh release create --help`, gh CLI on this machine, 2026-09-17
- HTTP behaviour of `/releases/latest`, `/releases/latest/download/…` and
  `api.github.com/rate_limit`, observed via `curl` against `goreleaser/goreleaser`,
  2026-09-16T18:40Z
