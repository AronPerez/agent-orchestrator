#!/usr/bin/env bash
# Cross-compile the Go `ao` binary (backend/cmd/ao) for every supported
# platform and drop each into the matching platform package's bin/ dir.
#
# Run this from any cwd before `npm publish`. It is the ONLY way the binaries
# get into the platform packages; they are gitignored and produced here, then
# shipped in each npm tarball via that package's `files` entry.
#
# CGO-free build (modernc.org/sqlite driver) so cross-compilation needs no C
# toolchain. cli.releaseRepo is deliberately NOT set, so it keeps its default
# (AgentWrapper/agent-orchestrator); the -X flags below only carry build
# identity, which is a different thing.
set -euo pipefail

# Repo layout: this script lives at <repo>/packages/build-binaries.sh.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BACKEND_DIR="${REPO_ROOT}/backend"

# pkg_dir : npm_os : npm_arch : GOOS : GOARCH : bin_name
TARGETS=(
  "ao-darwin-arm64:darwin:arm64:darwin:arm64:ao"
  "ao-darwin-x64:darwin:x64:darwin:amd64:ao"
  "ao-win32-x64:win32:x64:windows:amd64:ao.exe"
  "ao-linux-x64:linux:x64:linux:amd64:ao"
)

# Link-time build identity, for the same reason daemon-build.sh does it: Go's own
# VCS stamping is a silent no-op inside a linked git worktree, so a published
# binary built from one carries no identity at all — and `ao doctor`'s build-skew
# check can only report "cannot tell" about a CLI that cannot name its own build.
# Ask git directly instead; it works in a worktree.
build_stamp=""
if git_rev="$(git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null)"; then
  build_stamp="${git_rev}"
  if ! git -C "${REPO_ROOT}" diff --quiet HEAD 2>/dev/null; then
    build_stamp="${build_stamp}-dirty"
  fi
fi
stamp_pkg="github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
cli_pkg="github.com/aoagents/agent-orchestrator/backend/internal/cli"
cli_version="$(git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ldflags="-X ${stamp_pkg}.buildStamp=${build_stamp}"
ldflags="${ldflags} -X ${cli_pkg}.Version=${cli_version}"
ldflags="${ldflags} -X ${cli_pkg}.Commit=${build_stamp}"
ldflags="${ldflags} -X ${cli_pkg}.Date=${build_date}"

echo "Building ao binaries from ${BACKEND_DIR}/cmd/ao (${cli_version})"
for t in "${TARGETS[@]}"; do
  IFS=":" read -r pkg npm_os npm_arch goos goarch bin <<<"$t"
  out="${SCRIPT_DIR}/${pkg}/bin/${bin}"
  mkdir -p "${SCRIPT_DIR}/${pkg}/bin"
  echo "  -> ${pkg} (GOOS=${goos} GOARCH=${goarch}) -> bin/${bin}"
  (cd "${BACKEND_DIR}" && CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build -ldflags "${ldflags}" -o "${out}" ./cmd/ao)
  chmod 0755 "${out}"
done

echo "Done. Built binaries:"
for t in "${TARGETS[@]}"; do
  IFS=":" read -r pkg _ _ _ _ bin <<<"$t"
  file "${SCRIPT_DIR}/${pkg}/bin/${bin}"
done
