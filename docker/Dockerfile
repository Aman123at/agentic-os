# syntax=docker/dockerfile:1.7
#
# Targets (PLAN.md §6.1): `cli` and `ui`. Compose selects one with target: ${AOS_MODE}.
# BuildKit builds only the stages the chosen target needs, so `cli` never runs Node.

ARG GO_VERSION=1.27.1
ARG NODE_VERSION=24.21.0
ARG NODE_SHA256_ARM64=6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2
ARG NODE_SHA256_X64=fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6

# ---------------------------------------------------------------- Desktop (ui only)
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

FROM go-base AS aosd-cli
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/ ./cmd/aosd ./cmd/aos

FROM go-base AS aosd-ui
ARG TARGETOS TARGETARCH
COPY --from=desktop-build /src/desktop/dist/ internal/webui/dist/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags desktop -ldflags "-s -w" -o /out/ ./cmd/aosd ./cmd/aos

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
# aosd arranges the home layout (Protected dotfiles, ~/Shared) at every start.
RUN userdel -r ubuntu 2>/dev/null || true; \
    useradd --uid 1000 --user-group --create-home --shell /bin/bash aos \
 && rm -f /etc/apt/apt.conf.d/docker-clean \
 && mkdir -p /var/cache/aos/apt/archives/partial /var/cache/aos/apt/lists/partial /var/lib/aos /run/aos /shared \
 && chmod 0700 /var/lib/aos
COPY docker/rootfs/ /
RUN chmod 0440 /etc/sudoers.d/aos
WORKDIR /home/aos
EXPOSE 7700

FROM machine AS cli
COPY --from=aosd-cli /out/ /usr/local/bin/
ENV AOS_IMAGE_MODE=cli
ENTRYPOINT ["/usr/local/bin/aosd"]

FROM machine AS ui
COPY --from=aosd-ui /out/ /usr/local/bin/
ENV AOS_IMAGE_MODE=ui
ENTRYPOINT ["/usr/local/bin/aosd"]
