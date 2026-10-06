#!/usr/bin/env bash
# Fresh Debian/Ubuntu host (x64 or arm64) -> this fork's AO daemon as a systemd
# user service. Builds from source because the fork publishes no release assets;
# everything from the bundle down is upstream's own installer, untouched.
# Re-run to upgrade: the installer stages each build as a release and rolls back
# if the new daemon does not come up. Upstream's bootstrap-self-hosted.sh and a
# bare setup-self-hosted.sh install upstream's AO instead.
set -euo pipefail

usage() {
	printf '%s\n' 'Usage: bootstrap-fork-host.sh [--ref BRANCH_OR_TAG] [--repo URL] [--bundle PATH] [--lan]'
	printf '%s\n' 'Installs this fork of AO under ~/.ao/host as a systemd user service and prints pairing details.'
	printf '%s\n' '  --ref     branch or tag to build (default: develop)'
	printf '%s\n' '  --repo    git URL to build from (default: this fork on GitHub)'
	printf '%s\n' '  --bundle  install a prebuilt ao-host-linux-*.tar.gz instead of building'
	printf '%s\n' '  --lan     plain HTTP on the LAN instead of a Cloudflare quick tunnel'
}

ref=develop
repo=https://github.com/AronPerez/agent-orchestrator.git
bundle=""
tunnel=true
while (($#)); do
	case "$1" in
		--ref) ref="${2:?--ref needs a branch or tag}"; shift 2 ;;
		--repo) repo="${2:?--repo needs a git URL}"; shift 2 ;;
		--bundle) bundle="${2:?--bundle needs a path}"; shift 2 ;;
		--lan) tunnel=false; shift ;;
		-h|--help) usage; exit 0 ;;
		*) usage >&2; exit 2 ;;
	esac
done

[[ "$(id -u)" != 0 ]] || { printf '%s\n' 'Run as the host user, not root.' >&2; exit 1; }
[[ "$(uname -s)" == Linux ]] || { printf '%s\n' 'This bootstrap supports Linux only.' >&2; exit 1; }
case "$(uname -m)" in
	x86_64) arch=x64; go_arch=amd64 ;;
	aarch64|arm64) arch=arm64; go_arch=arm64 ;;
	*) printf 'Unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac
command -v apt-get >/dev/null || { printf '%s\n' 'This bootstrap requires apt-get (Debian/Ubuntu).' >&2; exit 1; }
[[ "$ref" =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ ]] || { printf '%s\n' 'Invalid --ref.' >&2; exit 2; }
[[ -z "$bundle" || -f "$bundle" ]] || { printf 'Bundle not found: %s\n' "$bundle" >&2; exit 1; }

sudo apt-get update
sudo apt-get install -y ca-certificates curl gh git python3
sudo loginctl enable-linger "$(id -un)"
systemctl --user show-environment >/dev/null || {
	printf '%s\n' 'A systemd user session is required for an always-on AO host.' >&2; exit 1;
}

if "$tunnel" && ! command -v cloudflared >/dev/null; then
	codename="$(. /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")"
	[[ -n "$codename" ]] || {
		printf '%s\n' 'Cannot read the distro codename from /etc/os-release; install cloudflared yourself or use --lan.' >&2
		exit 1
	}
	sudo install -d -m 0755 /usr/share/keyrings
	curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
	printf 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared %s main\n' "$codename" |
		sudo tee /etc/apt/sources.list.d/cloudflared.list >/dev/null
	sudo apt-get update
	sudo apt-get install -y cloudflared
fi

stage="$(mktemp -d)"
trap 'rm -rf -- "$stage"' EXIT

if [[ -n "$bundle" ]]; then
	# A prebuilt bundle only needs the installer, taken from the same ref.
	installer="$stage/setup-self-hosted.sh"
	raw="${repo%.git}"
	raw="${raw/github.com/raw.githubusercontent.com}"
	curl -fsSL "$raw/$ref/scripts/setup-self-hosted.sh" -o "$installer"
else
	sudo apt-get install -y build-essential pkg-config xz-utils
	tools="$HOME/.local/ao-build-tools"
	mkdir -p "$tools"
	go_version=1.27.1    # backend/go.mod
	node_version=22.23.2 # frontend/scripts/build-acp-runtime.mjs
	if [[ ! -x "$tools/go/bin/go" ]]; then
		curl -fsSL "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz" -o "$stage/go.tar.gz"
		expected="$(curl -fsSL "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz.sha256")"
		[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { printf '%s\n' 'Go checksum missing.' >&2; exit 1; }
		printf '%s  %s\n' "$expected" "$stage/go.tar.gz" | sha256sum -c -
		tar -xzf "$stage/go.tar.gz" -C "$tools"
	fi
	node_dir="node-v${node_version}-linux-${arch}"
	if [[ ! -x "$tools/$node_dir/bin/node" ]]; then
		curl -fsSL "https://nodejs.org/dist/v${node_version}/${node_dir}.tar.xz" -o "$stage/node.tar.xz"
		curl -fsSL "https://nodejs.org/dist/v${node_version}/SHASUMS256.txt" -o "$stage/SHASUMS256.txt"
		expected="$(awk -v name="${node_dir}.tar.xz" '$2 == name { print $1 }' "$stage/SHASUMS256.txt")"
		[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { printf '%s\n' 'Node checksum missing.' >&2; exit 1; }
		printf '%s  %s\n' "$expected" "$stage/node.tar.xz" | sha256sum -c -
		tar -xJf "$stage/node.tar.xz" -C "$tools"
	fi
	export PATH="$tools/go/bin:$tools/$node_dir/bin:$PATH"
	git clone --depth 1 --single-branch --branch "$ref" "$repo" "$stage/source"
	(
		cd "$stage/source/frontend"
		# Upstream's small-VM caps; the headless host never runs Electron, so skip
		# its binary download.
		export GOMAXPROCS=2 NODE_OPTIONS=--max-old-space-size=1536 ELECTRON_SKIP_BINARY_DOWNLOAD=1
		npm ci
		npm run build:daemon
		npm run build:tmux
		npm run build:acp-runtime
		npm run build:host
	)
	installer="$stage/source/scripts/setup-self-hosted.sh"
	bundle="$stage/source/frontend/dist-host/ao-host-linux-${arch}.tar.gz"
fi

args=(--bundle "$bundle")
"$tunnel" && args+=(--tunnel)
bash "$installer" "${args[@]}"

printf '\nDay-two commands: %s\n' "$HOME/.ao/host/current/resources/daemon/ao remote-host status"
printf 'Service logs: journalctl --user -u ao-self-hosted.service -n 100 --no-pager\n'
printf 'Upgrade: re-run this script with the same flags.\n'
printf "\nPair from this fork's desktop app (ignore the Settings → Remote hosts line above; that page exists only in upstream AO):\n"
printf 'Settings → General → turn on Remote hosts, then Add project → host selector → Add remote host, with the Address and Password printed above.\n'
