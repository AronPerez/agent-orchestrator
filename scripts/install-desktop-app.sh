#!/usr/bin/env bash
#
# install-desktop-app.sh — build, sign, and install the AO desktop app locally (macOS).
#
# Why this exists: an Electron app built without an Apple Developer ID (the
# default for a local `npm run make`) gets an ad-hoc signature that
#   1. macOS auto-update (Squirrel/ShipIt) refuses — it can't satisfy the
#      installed app's code requirement, so releases can't self-update; and
#   2. is frequently *broken* for a customized bundle ("code has no resources
#      but signature indicates they must be present"), so `open` dies with
#      launchd error 162.
# So local installs have to be done by hand: build, copy into /Applications,
# deep ad-hoc re-sign, strip the quarantine flag, launch. This wraps that.
#
# It also (by default) installs the app's bundled daemon as THE daemon:
# setup-self-hosted.sh --app copies it into ~/.ao/host/current, restarts the
# dev.aoagents.self-hosted LaunchAgent, and points ~/.ao/bin/ao and
# ~/.local/bin/ao at it. The app attaches to that same-build daemon instead of
# spawning its own copy, so app, daemon and CLI all run one commit. Skip with
# --no-daemon.
#
# Usage:
#   scripts/install-desktop-app.sh [options]
#
# Options:
#   --skip-build     Use the existing build in frontend/out (don't run `npm run make`).
#   --no-daemon      Don't install the daemon into ~/.ao/host (app-only update).
#   --no-launch      Install but don't open the app.
#   --dest DIR       Install directory (default: /Applications).
#   --print-release-repo  Print the GitHub owner/repo the build would bake into
#                    the app's updater, then exit.
#   -h, --help       Show this help.
#
# Notes:
#   * macOS only (needs codesign / xattr / open).
#   * The app's updater polls the GitHub repo baked at build time (AO_RELEASE_REPO;
#     forge defaults to upstream). Unset, this script derives owner/repo from this
#     checkout's origin remote, so a fork build never offers upstream's releases.
#   * If APPLE_SIGNING_IDENTITY is set, `npm run make` already signs with a real
#     identity, so the ad-hoc re-sign is skipped (it would clobber that).
#   * The app and the daemon must be built from the SAME commit for the identity
#     match — this script builds the app and reloads the daemon together, so keep
#     the working tree at the commit you want to run.
set -euo pipefail

# --- options ---------------------------------------------------------------
skip_build=0
reload_daemon=1
launch=1
print_release_repo=0
dest="/Applications"

while [ $# -gt 0 ]; do
  case "$1" in
    --skip-build) skip_build=1 ;;
    --no-daemon)  reload_daemon=0 ;;
    --no-launch)  launch=0 ;;
    --dest)       dest="${2:?--dest needs a directory}"; shift ;;
    --print-release-repo) print_release_repo=1 ;;
    -h|--help)    sed -n '3,43p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
  shift
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

# --- update feed -----------------------------------------------------------
# forge.config.ts bakes the updater's GitHub repo from AO_RELEASE_REPO and
# defaults to upstream, so a plain `npm run make` builds an app that polls
# upstream's releases. Derive owner/repo from this checkout's origin remote
# instead; an explicit AO_RELEASE_REPO still wins. Always returns 0: an
# unrecognized remote prints nothing and forge keeps its own default.
release_repo_from_remote() {
  local url="$1" path
  case "$url" in
    git@github.com:*) path="${url#git@github.com:}" ;;
    https://github.com/*|http://github.com/*|ssh://git@github.com/*) path="${url#*github.com/}" ;;
    *) return 0 ;;
  esac
  path="${path%/}"
  path="${path%.git}"
  if [[ "$path" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then printf '%s' "$path"; fi
}
if [ -z "${AO_RELEASE_REPO:-}" ]; then
  AO_RELEASE_REPO="$(release_repo_from_remote "$(git -C "$repo_root" remote get-url origin 2>/dev/null || true)")"
  [ -n "$AO_RELEASE_REPO" ] || echo "warning: could not derive owner/repo from the origin remote; forge will use its default release repo (upstream)." >&2
fi
if [ "$print_release_repo" -eq 1 ]; then printf '%s\n' "$AO_RELEASE_REPO"; exit 0; fi
[ -z "$AO_RELEASE_REPO" ] || export AO_RELEASE_REPO

# --- guards ----------------------------------------------------------------
[ "$(uname -s)" = "Darwin" ] || { echo "error: macOS only (uses codesign/xattr/open)." >&2; exit 1; }

frontend="${repo_root}/frontend"
[ -d "$frontend" ] || { echo "error: no frontend/ under ${repo_root}." >&2; exit 1; }

# electron-forge names its output dir <productName>-darwin-<arch>.
case "$(uname -m)" in
  arm64)  arch="arm64" ;;
  x86_64) arch="x64" ;;
  *) echo "error: unsupported arch $(uname -m)." >&2; exit 1 ;;
esac

log() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }

# --- 1. build --------------------------------------------------------------
if [ "$skip_build" -eq 0 ]; then
  log "Building app + daemon (npm run make; update feed: ${AO_RELEASE_REPO:-forge default})…"
  ( cd "$frontend" && npm run make )
else
  log "Skipping build (--skip-build)."
fi

# --- 2. locate the freshly built .app --------------------------------------
# Newest *.app under frontend/out/<name>-darwin-<arch>/ (not the makers' out/make).
app_src="$(
  find "${frontend}/out" -maxdepth 2 -type d -name '*.app' -path "*-darwin-${arch}/*" 2>/dev/null \
    | while read -r p; do printf '%s\t%s\n' "$(stat -f '%m' "$p")" "$p"; done \
    | sort -rn | head -1 | cut -f2-
)"
[ -n "$app_src" ] && [ -d "$app_src" ] || {
  echo "error: no built .app found under ${frontend}/out for arch ${arch}." >&2
  echo "       run without --skip-build, or check that 'npm run make' succeeded." >&2
  exit 1
}
app_name="$(basename "$app_src")"
app_dest="${dest%/}/${app_name}"
log "Built app: ${app_src}"

# --- 3. quit any running instance ------------------------------------------
# You can't cleanly replace a running .app, so ask it to quit first. Graceful
# (AppleScript quit) with a bounded wait, then a hard fallback.
#
# This has to happen BEFORE the daemon reload: an app-owned daemon from the old
# bundle still holds :3001, so a daemon kickstarted while it runs quietly falls
# back to an ephemeral port and publishes that in running.json — leaving anything
# pinned to :3001 (the LAN UI's vite proxy) talking to the stale daemon.
bundle_id="$(/usr/libexec/PlistBuddy -c 'Print CFBundleIdentifier' "$app_src/Contents/Info.plist" 2>/dev/null || true)"
if [ -n "$bundle_id" ] && pgrep -f "${app_dest}/Contents/MacOS/" >/dev/null 2>&1; then
  log "Quitting the running app (${bundle_id})…"
  osascript -e "tell application id \"${bundle_id}\" to quit" >/dev/null 2>&1 || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    pgrep -f "${app_dest}/Contents/MacOS/" >/dev/null 2>&1 || break
    sleep 0.5
  done
  pkill -f "${app_dest}/Contents/MacOS/" 2>/dev/null || true
fi

# --- 4. stop a daemon the old app left behind ------------------------------
# Under AO_KEEP_DAEMON the old bundle's daemon outlives the app and keeps :3001,
# which would make the host install below refuse to start its own. Sessions
# survive: their pty-hosts are adopted by the next daemon.
old_daemon="${app_dest}/Contents/Resources/daemon/ao"
if [ "$reload_daemon" -eq 1 ] && pgrep -f "${old_daemon} daemon" >/dev/null 2>&1; then
  log "Stopping the old app-bundled daemon…"
  "$old_daemon" stop || { echo "error: could not stop ${old_daemon}." >&2; exit 1; }
fi

# --- 5. install ------------------------------------------------------------
log "Installing to ${app_dest}…"
rm -rf "$app_dest"
cp -R "$app_src" "$dest/"

# --- 6. sign ---------------------------------------------------------------
if [ -n "${APPLE_SIGNING_IDENTITY:-}" ]; then
  log "APPLE_SIGNING_IDENTITY set — keeping the real signature from the build."
else
  # Deep ad-hoc re-sign. Fixes the broken default electron-forge ad-hoc signature
  # (Identifier=Electron / stale resource rules) that makes launchd reject the app
  # with error 162. Apple Silicon requires a *valid* signature even for local apps.
  log "Ad-hoc re-signing the bundle (local unsigned build)…"
  codesign --force --deep --sign - "$app_dest"
  codesign --verify --deep "$app_dest" || { echo "error: signature verify failed after re-sign." >&2; exit 1; }
fi

# --- 7. de-quarantine ------------------------------------------------------
# Strip com.apple.quarantine so Gatekeeper doesn't block a locally-built app.
xattr -dr com.apple.quarantine "$app_dest" 2>/dev/null || true

# --- 8. install the daemon into ~/.ao/host --------------------------------
if [ "$reload_daemon" -eq 1 ]; then
  log "Installing the bundled daemon into ~/.ao/host (setup-self-hosted.sh --app)…"
  "${script_dir}/setup-self-hosted.sh" --app "$app_dest"
fi

# --- 9. launch -------------------------------------------------------------
if [ "$launch" -eq 1 ]; then
  log "Launching…"
  open "$app_dest"
fi

log "Done: ${app_dest}"
