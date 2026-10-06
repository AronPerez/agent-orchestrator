#!/usr/bin/env bash
# Hermetic check for bootstrap-fork-host.sh: every external command is a stub
# on a private PATH, the toolchain is pre-seeded so nothing downloads, and the
# installer is a recorder, so what is asserted is the orchestration — which
# repo and ref get cloned, which build steps run, and exactly what the
# installer is handed.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/bootstrap-fork-host.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
export TEST_LOG_DIR="$tmp/log" TEST_RECORDER="$tmp/recorder.sh" TEST_SETUP_ARGS="$tmp/setup-args"
mkdir -p "$tmp/bin" "$TEST_LOG_DIR" "$tmp/home"
export HOME="$tmp/home"

for tool in awk bash cat chmod cp dirname grep mkdir mktemp readlink rm sed tar; do
	ln -s "$(command -v "$tool")" "$tmp/bin/$tool"
done
printf '%s\n' '#!/bin/sh' 'case "$1" in -u) echo "${TEST_UID:-1000}" ;; -un) echo ao ;; esac' > "$tmp/bin/id"
printf '%s\n' '#!/bin/sh' 'case "$1" in -s) echo Linux ;; -m) echo "${TEST_MACHINE:-x86_64}" ;; esac' > "$tmp/bin/uname"
# sudo runs its command; every privileged command below is itself a stub.
printf '%s\n' '#!/bin/sh' 'exec "$@"' > "$tmp/bin/sudo"
for stub in apt-get loginctl; do
	printf '%s\n' '#!/bin/sh' "echo \"\$*\" >> \"\$TEST_LOG_DIR/$stub.log\"" > "$tmp/bin/$stub"
done
printf '%s\n' '#!/bin/sh' 'exit 0' > "$tmp/bin/systemctl"
# git clone plants a source tree whose installer is the recorder.
printf '%s\n' '#!/bin/sh' 'echo "$*" >> "$TEST_LOG_DIR/git.log"' \
	'if [ "$1" = clone ]; then' \
	'  for a; do dest=$a; done' \
	'  mkdir -p "$dest/scripts" "$dest/frontend"' \
	'  cp "$TEST_RECORDER" "$dest/scripts/setup-self-hosted.sh"' \
	'fi' > "$tmp/bin/git"
# npm run build:host produces the bundle the real script would.
printf '%s\n' '#!/bin/sh' 'echo "$*" >> "$TEST_LOG_DIR/npm.log"' \
	'if [ "$1" = run ] && [ "$2" = build:host ]; then' \
	'  mkdir -p dist-host && : > "dist-host/ao-host-linux-${TEST_ARCH:-x64}.tar.gz"' \
	'fi' > "$tmp/bin/npm"
# curl -o FILE delivers the recorder (the installer download on the --bundle path).
printf '%s\n' '#!/bin/sh' 'echo "$*" >> "$TEST_LOG_DIR/curl.log"' \
	'out=""; prev=""' \
	'for a; do [ "$prev" = -o ] && out=$a; prev=$a; done' \
	'[ -n "$out" ] && cp "$TEST_RECORDER" "$out"' \
	'exit 0' > "$tmp/bin/curl"
printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$*" > "$TEST_SETUP_ARGS"' \
	'printf "Pair this host\n\nAddress: http://192.0.2.1:3011\n\nPassword: test-secret\n\n"' > "$TEST_RECORDER"
chmod +x "$tmp/bin/id" "$tmp/bin/uname" "$tmp/bin/sudo" "$tmp/bin/apt-get" "$tmp/bin/loginctl" \
	"$tmp/bin/systemctl" "$tmp/bin/git" "$tmp/bin/npm" "$tmp/bin/curl" "$TEST_RECORDER"

seed_toolchain() {
	local arch=$1 tools="$HOME/.local/ao-build-tools"
	mkdir -p "$tools/go/bin" "$tools/node-v22.23.2-linux-$arch/bin"
	printf '%s\n' '#!/bin/sh' > "$tools/go/bin/go"
	printf '%s\n' '#!/bin/sh' > "$tools/node-v22.23.2-linux-$arch/bin/node"
	chmod +x "$tools/go/bin/go" "$tools/node-v22.23.2-linux-$arch/bin/node"
}
run() { env PATH="$tmp/bin" /bin/bash "$script" "$@" > "$tmp/out" 2>&1; }
fail() { cat "$tmp/out" >&2; printf '%s\n' "$1" >&2; exit 1; }

case "${1:-}" in
	default)
		seed_toolchain x64
		# cloudflared already present: the tunnel default must not touch apt repos.
		printf '%s\n' '#!/bin/sh' > "$tmp/bin/cloudflared"; chmod +x "$tmp/bin/cloudflared"
		TEST_ARCH=x64 run || fail 'default run failed'
		grep -qE '^clone .*--branch develop https://github.com/AronPerez/agent-orchestrator.git ' "$TEST_LOG_DIR/git.log" || fail 'did not clone the fork at develop'
		for step in '^ci$' '^run build:daemon$' '^run build:tmux$' '^run build:acp-runtime$' '^run build:host$'; do
			grep -qE "$step" "$TEST_LOG_DIR/npm.log" || fail "missing npm step $step"
		done
		[[ "$(cat "$TEST_SETUP_ARGS")" == "--bundle "*"/frontend/dist-host/ao-host-linux-x64.tar.gz --tunnel" ]] || fail "installer args: $(cat "$TEST_SETUP_ARGS")"
		grep -q 'enable-linger ao' "$TEST_LOG_DIR/loginctl.log" || fail 'linger not enabled'
		grep -q 'install -y build-essential' "$TEST_LOG_DIR/apt-get.log" || fail 'build prerequisites not installed'
		! grep -q cloudflared "$TEST_LOG_DIR/apt-get.log" || fail 'reinstalled a present cloudflared'
		grep -q 'Add project' "$tmp/out" || fail 'missing the fork pairing hint'
		grep -q 'Address: http://192.0.2.1:3011' "$tmp/out" || fail 'installer output not shown'
		;;
	lan-ref)
		seed_toolchain arm64
		TEST_MACHINE=aarch64 TEST_ARCH=arm64 run --lan --ref my-branch || fail 'lan run failed'
		grep -qE '^clone .*--branch my-branch ' "$TEST_LOG_DIR/git.log" || fail 'did not clone the requested ref'
		[[ "$(cat "$TEST_SETUP_ARGS")" == "--bundle "*"/frontend/dist-host/ao-host-linux-arm64.tar.gz" ]] || fail "installer args: $(cat "$TEST_SETUP_ARGS")"
		! grep -q cloudflared "$TEST_LOG_DIR/apt-get.log" || fail '--lan installed cloudflared'
		;;
	bundle)
		: > "$tmp/prebuilt.tar.gz"
		run --lan --bundle "$tmp/prebuilt.tar.gz" || fail 'bundle run failed'
		[[ ! -e "$TEST_LOG_DIR/git.log" && ! -e "$TEST_LOG_DIR/npm.log" ]] || fail '--bundle still cloned or built'
		grep -q 'raw.githubusercontent.com/AronPerez/agent-orchestrator/develop/scripts/setup-self-hosted.sh' "$TEST_LOG_DIR/curl.log" || fail 'installer not fetched from the fork at the ref'
		[[ "$(cat "$TEST_SETUP_ARGS")" == "--bundle $tmp/prebuilt.tar.gz" ]] || fail "installer args: $(cat "$TEST_SETUP_ARGS")"
		;;
	unsupported)
		if TEST_MACHINE=i686 run; then fail 'accepted an unsupported architecture'; fi
		grep -q 'Unsupported architecture: i686' "$tmp/out" || fail 'wrong refusal message'
		[[ ! -e "$TEST_LOG_DIR/apt-get.log" ]] || fail 'touched apt before refusing'
		if TEST_UID=0 run; then fail 'accepted root'; fi
		;;
	*) printf 'Usage: %s {default|lan-ref|bundle|unsupported}\n' "$0" >&2; exit 2 ;;
esac
printf 'PASS %s\n' "$1"
