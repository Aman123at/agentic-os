# syntax=docker/dockerfile:1.7
#
# One runtime image (PLAN.md §6.1, M6.10/M6.11): the `aos` target always carries
# the embedded Desktop, Mode is a runtime config.yml key, and the Browser app's
# headless shell is no longer baked in — `sudo aos browser install` fetches it on
# demand (ADR-0008, M6.11). There is one runtime stage and no build-time fork.
#
# This Compose image is a curated convenience: the `machine` stage below apt-installs
# ~25 baseline packages and a pinned Node, free in a disposable container. A native
# install carries none of that (M6.15) — `install.sh` ships only `aosd`, and Agents
# install what a Task needs through the Install Ledger, which persists natively.

ARG GO_VERSION=1.27.1
ARG NODE_VERSION=24.21.0
ARG NODE_SHA256_ARM64=6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2
ARG NODE_SHA256_X64=fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6

# ---------------------------------------------------------------- Desktop
FROM --platform=$BUILDPLATFORM node:24-slim AS desktop-build
WORKDIR /src/desktop
COPY desktop/package.json desktop/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY desktop/ ./
RUN npm run build

# ---------------------------------------------------------------- Go
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS go-base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY gen/ gen/
COPY cmd/ cmd/
COPY internal/ internal/
COPY tools/ tools/

# Linux unit tests (CI only): `go run ./tools/ci unit-linux` exports this target,
# so the _linux.go code the macOS Host can't run is tested too. The run records
# go test's exit status instead of failing, so its report comes out either way.
FROM go-base AS go-test-run
# The plan, for the tests that check the code still matches it (the version the
# Machine reports). Copied only into this stage, so a doc edit never invalidates
# a build.
COPY docs/ docs/
# The installer, for the release stage's asset-name drift check (M6.18): it
# asserts install.sh still fetches the names the release stage produces.
COPY install.sh install.sh
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir /report; \
    CGO_ENABLED=0 go test -count=1 -json ./... >/report/test.json 2>/report/stderr.txt; \
    echo $? >/report/status

FROM scratch AS go-test
COPY --from=go-test-run /report/ /

# One binary carries the Desktop; there is no build tag (M6.10). Assets() serves
# the embedded dist only when index.html is present, which it is once Vite has
# built it into internal/webui/dist/.
FROM go-base AS aosd
ARG TARGETOS TARGETARCH
COPY --from=desktop-build /src/desktop/dist/ internal/webui/dist/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/ ./cmd/aosd

# ---------------------------------------------------------------- Node.js (baseline software)
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS node-dist
ARG TARGETARCH NODE_VERSION NODE_SHA256_ARM64 NODE_SHA256_X64
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    case "$TARGETARCH" in \
      arm64) arch=arm64; sum=$NODE_SHA256_ARM64 ;; \
      amd64) arch=x64;   sum=$NODE_SHA256_X64 ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo /tmp/node.tar.xz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${arch}.tar.xz"; \
    echo "$sum  /tmp/node.tar.xz" | sha256sum -c -; \
    mkdir -p /opt/node; \
    tar -xJf /tmp/node.tar.xz -C /opt/node --strip-components=1 --no-same-owner; \
    rm -rf /opt/node/include /opt/node/share /opt/node/*.md /opt/node/LICENSE

# ---------------------------------------------------------------- The Machine
FROM ubuntu:24.04 AS machine
ENV LANG=C.UTF-8 TZ=UTC
# Baseline software (PLAN.md §6.3). docker-clean is still active here, so the
# image keeps no .deb files; the runtime apt config is installed afterwards.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      bash coreutils procps less nano vim-tiny htop file tree jq sudo apt-utils tzdata \
      curl wget ca-certificates iproute2 iputils-ping openssh-client \
      git rsync rclone zip unzip xz-utils p7zip-full \
      python3 python3-venv python3-pip pipx \
 && rm -rf /var/lib/apt/lists/*
COPY --from=node-dist /opt/node/ /usr/local/
# Ubuntu images ship a `ubuntu` user with uid 1000; the Machine's user is `aos`.
# aosd arranges the home layout (Protected dotfiles) at every start.
# ~/Downloads is where downloads land and the Desktop's Downloads stack looks;
# a new home volume is seeded with it from the image.
RUN userdel -r ubuntu 2>/dev/null || true; \
    useradd --uid 1000 --user-group --create-home --shell /bin/bash aos \
 && install -d -o aos -g aos /home/aos/Downloads \
 && rm -f /etc/apt/apt.conf.d/docker-clean \
 && mkdir -p /var/cache/aos/apt/archives/partial /var/cache/aos/apt/lists/partial /var/lib/aos /run/aos \
 && chmod 0700 /var/lib/aos
COPY docker/rootfs/ /
# aos and the Agent's rm shim are links to aosd, one binary for all three.
RUN chmod 0440 /etc/sudoers.d/aos \
 && mkdir -p /usr/local/lib/aos/agent-bin \
 && ln -s aosd /usr/local/bin/aos \
 && ln -s /usr/local/bin/aos /usr/local/lib/aos/agent-bin/rm
WORKDIR /home/aos
EXPOSE 7700

# The Browser app carries no Chromium and no browser libraries: `sudo aos browser
# install` fetches the headless shell on demand and its DT_NEEDED check names any
# library the box is missing (ADR-0008, M6.11). One runtime stage, no fork.
FROM machine AS aos
COPY --from=aosd /out/ /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/aosd"]
