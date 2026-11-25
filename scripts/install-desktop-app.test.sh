#!/usr/bin/env bash
# The one piece of install-desktop-app.sh that is testable off a Mac: which
# GitHub owner/repo it bakes into the app's update feed. --print-release-repo
# answers before the macOS guard, with `git` stubbed to return each remote shape.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/install-desktop-app.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$TEST_REMOTE_URL"' > "$tmp/bin/git"
chmod +x "$tmp/bin/git"

print_repo() {
	env -u AO_RELEASE_REPO PATH="$tmp/bin:$PATH" TEST_REMOTE_URL="$1" /bin/bash "$script" --print-release-repo 2> "$tmp/err"
}
expect() {
	[[ "$1" == "$2" ]] || { printf 'expected %q, got %q\n' "$2" "$1" >&2; exit 1; }
}

case "${1:-}" in
	env)
		# An explicit AO_RELEASE_REPO wins over whatever the remote says.
		out="$(env PATH="$tmp/bin:$PATH" TEST_REMOTE_URL="https://github.com/AronPerez/agent-orchestrator.git" AO_RELEASE_REPO="someone/else" /bin/bash "$script" --print-release-repo)"
		expect "$out" "someone/else"
		;;
	https)
		expect "$(print_repo 'https://github.com/AronPerez/agent-orchestrator.git')" "AronPerez/agent-orchestrator"
		expect "$(print_repo 'https://github.com/AronPerez/agent-orchestrator')" "AronPerez/agent-orchestrator"
		;;
	ssh)
		expect "$(print_repo 'git@github.com:Owner/Repo.git')" "Owner/Repo"
		expect "$(print_repo 'ssh://git@github.com/Owner/Repo.git')" "Owner/Repo"
		;;
	unknown)
		# Not GitHub: say so, print nothing, and let forge keep its own default.
		expect "$(print_repo 'https://example.com/x/y.git')" ""
		grep -q 'could not derive' "$tmp/err" || { cat "$tmp/err" >&2; printf '%s\n' 'missing warning for an unrecognized remote' >&2; exit 1; }
		;;
	build)
		# The install uses only the .app, so it packages instead of making the
		# zip and dmg. prepackage must build everything premake does, or the
		# installed app would miss a bundled resource.
		frontend="$(cd "$(dirname "$script")/../frontend" && pwd)"
		node -e 'const s = require(process.argv[1]).scripts; if (s.prepackage !== s.premake) { console.error(`prepackage differs from premake:\n  ${s.prepackage}\n  ${s.premake}`); process.exit(1); }' "$frontend/package.json"
		if [[ "$(uname -s)" != Darwin ]]; then printf 'PASS %s (package.json only; the build step needs macOS)\n' "$1"; exit 0; fi
		printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$*" > "$TEST_NPM_ARGS"' 'exit 1' > "$tmp/bin/npm"
		chmod +x "$tmp/bin/npm"
		if env PATH="$tmp/bin:$PATH" TEST_REMOTE_URL="https://github.com/AronPerez/agent-orchestrator.git" TEST_NPM_ARGS="$tmp/npm-args" \
			/bin/bash "$script" --dest "$tmp/dest" --no-daemon --no-launch > /dev/null 2>&1; then
			printf '%s\n' 'expected the stubbed build to stop the install' >&2; exit 1
		fi
		expect "$(cat "$tmp/npm-args")" "run package"
		;;
	*) printf 'Usage: %s {env|https|ssh|unknown|build}\n' "$0" >&2; exit 2 ;;
esac
printf 'PASS %s\n' "$1"
