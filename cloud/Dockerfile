# syntax=docker/dockerfile:1

ARG BUILDPLATFORM
ARG TARGETOS=linux
ARG TARGETARCH
ARG EXPAT_BUILD_IMAGE=debian:trixie-slim
ARG BROTLI_BUILD_IMAGE=debian:trixie-slim

FROM --platform=${BUILDPLATFORM} golang:1.27.1-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/ao-cloud ./cmd/ao-cloud && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/ao-cloud-migrate ./cmd/ao-cloud-migrate && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/ao-cloud-healthcheck ./cmd/ao-cloud-healthcheck && \
    for worker_arch in amd64 arm64; do \
        CGO_ENABLED=0 GOOS=linux GOARCH="${worker_arch}" \
        go build -trimpath -ldflags="-s -w" -o "/out/linux-${worker_arch}/ao-worker" ./cmd/ao-worker && \
        CGO_ENABLED=0 GOOS=linux GOARCH="${worker_arch}" \
        go build -trimpath -ldflags="-s -w" -o "/out/linux-${worker_arch}/ao" ./cmd/ao-cloud-agent || exit 1; \
    done && \
    cp "/out/linux-${TARGETARCH}/ao-worker" /out/ao-worker && \
    cp "/out/linux-${TARGETARCH}/ao" /out/ao

# Trixie has no fix for CVE-2026-77214 yet. Rebuild Debian's pinned Expat
# 2.9.0 source on Trixie instead of mixing unstable runtime dependencies.
# Remove this stage once Trixie's libexpat1 includes the upstream bounds check.
FROM ${EXPAT_BUILD_IMAGE} AS expat-build
WORKDIR /build
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
        build-essential debhelper docbook-to-man fakeroot && \
    rm -rf /var/lib/apt/lists/*
# Pin the control file and Debian patch archive by snapshot identity and SHA-256.
ADD --checksum=sha256:b5ad165baca0f2f2ac2a02b6b4b66d68ee890f582df398da9f86399679ca9c29 \
    https://snapshot.debian.org/file/eefe91ce34a88026a1a7c8bfe08d990fa156890d/expat_2.9.0-1.dsc ./
ADD --checksum=sha256:6e8ce7b52ebbaa423c59becc7cf1e6875c42e49228c062ac8123c10e700cec27 \
    https://deb.debian.org/debian/pool/main/e/expat/expat_2.9.0.orig.tar.gz ./
ADD --checksum=sha256:d23e632b05f10e269ee5426ff1ea962b4aec17fcd179c4dec14390a6793da764 \
    https://snapshot.debian.org/file/9ea857818f1c38c4a718a740c954e35c725d3dc5/expat_2.9.0-1.debian.tar.xz ./
COPY test/expat-buffer-bounds.c /build/
RUN dpkg-source -x expat_2.9.0-1.dsc source && \
    cd source && \
    dpkg-buildpackage -b -us -uc -Pstage1,noudeb --jobs=2 && \
    make -C build check && \
    cd /build && \
    dpkg -i libexpat1_2.9.0-1_*.deb libexpat1-dev_2.9.0-1_*.deb && \
    mkdir /out && \
    cp libexpat1_2.9.0-1_*.deb /out/libexpat1.deb && \
    cc -Wall -Wextra -Werror expat-buffer-bounds.c -lexpat -o /out/expat-buffer-bounds && \
    /out/expat-buffer-bounds

# Trixie's base libbrotli1 (1.1.0-2) lacks the CVE-2026-111497 decoder fix,
# upstream commit 0ed4268a44. Debian's Security Team backported it as
# Detect-and-report-invalid-stream-earlier.patch in 1.1.0-2+deb13u1
# (trixie-security, 2026-10-23). Rebuild that source package on Trixie so the
# image does not depend on which point release the base image has reached.
# Remove this stage once Trixie's base libbrotli1 is at or past 1.1.0-2+deb13u1.
FROM ${BROTLI_BUILD_IMAGE} AS brotli-build
WORKDIR /build
RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
        build-essential cmake debhelper fakeroot && \
    rm -rf /var/lib/apt/lists/*
# Pin every source file by snapshot.debian.org identity and SHA-256. Snapshot
# files are content-addressed by SHA-1 and never change or expire, unlike
# GitHub's generated commit .patch output or superseded pool files.
ADD --checksum=sha256:64a1cd7422150e5c639d9892d6ffa837438b341f521e68ac4e75f6ef4eca49a2 \
    https://snapshot.debian.org/file/a3d0de606950140a8d02a17dc643e97743ce3093/brotli_1.1.0-2+deb13u1.dsc ./
ADD --checksum=sha256:10973f4b4199eafa1d5735ef661ddb2ec2f97319ee9fd1824d4aabe08cff5265 \
    https://snapshot.debian.org/file/c1a06bd9cb912a8b5f07b2bd504af78ec6d0335c/brotli_1.1.0.orig.tar.gz ./
ADD --checksum=sha256:848994612c6eb5e413d7df0e307ec7661855f5383c8cd10507e890fb7ee809ff \
    https://snapshot.debian.org/file/01a075ff6eea245f1e32fea5ab42dcae8e9ff9a2/brotli_1.1.0-2+deb13u1.debian.tar.xz ./
RUN dpkg-source -x brotli_1.1.0-2+deb13u1.dsc source && \
    cd source && \
    grep -q 'FORMAT_, BLOCK_SWITCH, -17' c/include/brotli/decode.h && \
    dpkg-buildpackage -b -us -uc -Pnopython --jobs=2 && \
    cd /build && \
    test "$(dpkg-deb -f libbrotli1_1.1.0-2+deb13u1_*.deb Version)" = 1.1.0-2+deb13u1 && \
    mkdir /out && \
    cp libbrotli1_1.1.0-2+deb13u1_*.deb /out/libbrotli1.deb

FROM node:22-trixie-slim AS worker
ARG TARGETARCH
ARG CLAUDE_CODE_VERSION=2.1.287
ARG CODEX_VERSION=0.147.0
ARG CURSOR_AGENT_VERSION=2026.08.11-e8db854
# OpenCode v2 ships as a NEW package, @opencode/cli (v1 was opencode-ai, whose
# `latest` never crosses to v2). Pinned to the v2 major; AO's opencode adapter is
# ported to v2 and gates a too-old binary loudly. Keep in lockstep with
# coder/Sandbox.Dockerfile.
ARG OPENCODE_VERSION=2
ARG GH_VERSION=2.97.0
RUN apt-get update && \
    apt-get upgrade --yes && \
    apt-get install --yes --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        openssh-client \
        procps \
        tar && \
    case "${TARGETARCH}" in \
        amd64|arm64) gh_arch="${TARGETARCH}" ;; \
        *) echo "unsupported GitHub CLI architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl --fail --location --silent --show-error \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${gh_arch}.tar.gz" \
        | tar --strip-components=2 -xzf - -C /usr/local/bin \
            "gh_${GH_VERSION}_linux_${gh_arch}/bin/gh" && \
    rm -rf /var/lib/apt/lists/* && \
    npm install --global \
        "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
        "@agentclientprotocol/claude-agent-acp@0.70.0" \
        "@openai/codex@${CODEX_VERSION}" \
        "@opencode/cli@${OPENCODE_VERSION}" && \
    claude --version && \
    test -x "$(command -v claude-agent-acp)" && \
    codex --version && \
    opencode --version && \
    gh --version && \
    groupadd --gid 10001 ao-worker && \
    useradd --uid 10001 --gid ao-worker --home-dir /workspace/.ao/home --shell /bin/bash ao-worker && \
    mkdir -p /workspace/repository /workspace/.ao/home && \
    chown -R ao-worker:ao-worker /workspace
RUN case "${TARGETARCH}" in \
        amd64) cursor_arch=x64 ;; \
        arm64) cursor_arch=arm64 ;; \
        *) echo "unsupported Cursor Agent architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    mkdir -p "/opt/cursor-agent/${CURSOR_AGENT_VERSION}" && \
    curl --fail --location --silent --show-error \
        "https://downloads.cursor.com/lab/${CURSOR_AGENT_VERSION}/linux/${cursor_arch}/agent-cli-package.tar.gz" \
        | tar --strip-components=1 -xzf - -C "/opt/cursor-agent/${CURSOR_AGENT_VERSION}" && \
    ln -s "/opt/cursor-agent/${CURSOR_AGENT_VERSION}/cursor-agent" /usr/local/bin/cursor-agent && \
    cursor-agent --version
# The agent browser ao-worker starts on first use: headless Chromium, fonts that
# render CJK, Arabic and Devanagari pages instead of boxes, and the agent-browser
# CLI. Only the local Docker provider runs sessions in this image, so
# compose.yaml sets AGENT_BROWSER=1; hosted sessions run in the Coder and
# Freestyle images, and leaving Chromium out keeps its CVEs out of the published
# worker scan. agent-browser version and sha256 pins: keep in lockstep with
# frontend/scripts/prepare-agent-browser.mjs.
ARG AGENT_BROWSER=0
RUN if [ "${AGENT_BROWSER}" != 1 ]; then exit 0; fi && \
    apt-get update && \
    apt-get install --yes --no-install-recommends \
        chromium \
        fonts-liberation \
        fonts-noto-cjk \
        fonts-noto-color-emoji \
        fonts-noto-core && \
    rm -rf /var/lib/apt/lists/* && \
    chromium --version && \
    agent_browser_version=0.38.1 && \
    case "${TARGETARCH}" in \
        amd64) agent_browser_target=linux-x64 \
            agent_browser_sha256=5100149a1903211c889de4e545bf36d90803740cea4f99aa22651649f9205ea1 ;; \
        arm64) agent_browser_target=linux-arm64 \
            agent_browser_sha256=937b315ee0761e8a62f7950ddcfef9b3d3d8e8d5eb9c9d2bf9e23e5725664511 ;; \
        *) echo "unsupported agent-browser architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl --fail --location --silent --show-error -o /usr/local/bin/agent-browser \
        "https://github.com/vercel-labs/agent-browser/releases/download/v${agent_browser_version}/agent-browser-${agent_browser_target}" && \
    echo "${agent_browser_sha256}  /usr/local/bin/agent-browser" | sha256sum -c - && \
    chmod 0755 /usr/local/bin/agent-browser && \
    agent-browser --version
RUN --mount=type=bind,from=expat-build,source=/out,target=/tmp/expat-fix \
    dpkg -i /tmp/expat-fix/libexpat1.deb && \
    /tmp/expat-fix/expat-buffer-bounds && \
    test "$(dpkg-query -W -f='${Version}' libexpat1)" = 2.9.0-1
# Install the rebuilt package only over an older libbrotli1, so a newer Trixie
# security update pulled in by apt-get upgrade above is never downgraded.
RUN --mount=type=bind,from=brotli-build,source=/out,target=/tmp/brotli-fix \
    if dpkg --compare-versions "$(dpkg-query -W -f='${Version}' libbrotli1)" lt 1.1.0-2+deb13u1; then \
        dpkg -i /tmp/brotli-fix/libbrotli1.deb; \
    fi && \
    dpkg --compare-versions "$(dpkg-query -W -f='${Version}' libbrotli1)" ge 1.1.0-2+deb13u1 && \
    curl --version | grep -q brotli
COPY --from=build --chown=ao-worker:ao-worker /out/ao-worker /ao-worker
COPY --from=build --chown=ao-worker:ao-worker /out/ao /usr/local/bin/ao
USER ao-worker
WORKDIR /workspace/repository
ENTRYPOINT ["/ao-worker"]

FROM gcr.io/distroless/static-debian12:nonroot AS control-plane
COPY --from=build --chown=nonroot:nonroot /out/ao-cloud /ao-cloud
COPY --from=build --chown=nonroot:nonroot /out/ao-cloud-migrate /ao-cloud-migrate
COPY --from=build --chown=nonroot:nonroot /out/ao-cloud-healthcheck /ao-cloud-healthcheck
# Workers are uploaded into sandboxes, not run here. /ao-worker and /ao are
# always the linux/amd64 build (the contract every provider and bake script
# relies on); the linux/arm64 build sits beside them for Coder workspaces on
# arm64 hosts, which the control plane detects and serves per architecture.
COPY --from=build --chown=nonroot:nonroot /out/linux-amd64/ao-worker /ao-worker
COPY --from=build --chown=nonroot:nonroot /out/linux-amd64/ao /ao
COPY --from=build --chown=nonroot:nonroot /out/linux-arm64/ao-worker /ao-worker-linux-arm64
COPY --from=build --chown=nonroot:nonroot /out/linux-arm64/ao /ao-linux-arm64
EXPOSE 8080
ENTRYPOINT ["/ao-cloud"]
