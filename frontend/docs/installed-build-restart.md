# Restarting after an external desktop install

An installed bundle and a running Electron process have separate lifetimes.
Replacing the bundle does not replace main's loaded JavaScript or a live
renderer. The old Fleet gate in the November 14 incident could therefore stay
in memory even though both the app and host daemon files had been updated.

## Install paths

| Path | Existing restart behavior |
| --- | --- |
| `src/main/auto-updater.ts` and `mac-differential-v2-updater.ts` | Download/stage through electron-updater; explicit install uses `quitAndInstall(false, true)`. macOS ShipIt swaps the bundle and relaunches; `update-quit.ts`, `update-relaunch-flag.ts` and `mac-update-progress.ts` coordinate shutdown/startup. |
| DMG (`scripts/build-mac-dmg.mjs`), extracted ZIP or manual bundle copy | DMG creation only supplies the app and Applications shortcut. Finder or another copier can replace the bundle independently of the running process. |
| `../scripts/install-desktop-app.sh` | Tries to quit the exact destination process, then removes/copies/signs the app, optionally updates the host daemon and opens the app. The quit guard is installer-specific. |
| Repair PKG (`scripts/build-mac-repair-installer.mjs`, `native/repair-installer/guard.sh`) | Refuses a running AO/ShipIt process and declares a must-close app. |
| `src/main/relocation.ts` / `app.moveToApplicationsFolder()` | A startup-time copy/handoff policy based on installed versions; no continuous running-build comparison. |
| `../backend/internal/cli/start.go` | Bootstrap downloads: macOS clears/unpacks `~/.ao/staging`; Windows runs NSIS; Linux replaces the stable AppImage path. `open`/single-instance activation alone cannot refresh an already-running Electron process. |
| NSIS, deb/rpm, external installers | Their own process handling applies. AO previously had no independent runtime check after their writes. |
| `../scripts/setup-self-hosted.sh`, including bootstrap wrappers and `--app` | Copies host resources into `~/.ao/host/releases/<stamp>`, switches `host/current` and restarts the daemon service. `--install-only` only switches files/links. Neither path relaunches Electron. |
| `ao-desktop-dev` workflow | Runs source builds; main/preload changes require a complete dev-process restart. It does not itself replace the installed bundle. |

## Runtime guard

`installed-build.ts` pins the startup archive's SHA-256 in memory. Main also
records its version, revision and a per-Forge-invocation token baked into both
main and renderer. The token distinguishes rebuilds at the same dirty commit.
Electron's `original-fs` reads the physical archive, avoiding its cached ASAR
index. File metadata caches unchanged hashes. A bounded ASAR header/length
check rejects truncated copies; two matching observations confirm a restart
target. The first different or unreadable observation immediately suspends
updater handoffs, before settling or reading worker state. Identity reads are
synchronous at automatic native handoff, explicit install, and retry relaunch,
so those paths cannot race ahead of the watcher notification. Unchanged files
use the metadata hash cache; changed files are hashed in bounded chunks.

Main watches the resources directory and, on macOS, the enclosing bundle's
parent. A 30-second timer reattaches watches and checks again; focus and resume
also check. Each check has a two-second follow-up. AppImage uses its stable
outer file rather than the old mounted archive. Unpackaged development keeps
its existing reload workflow.

An installed-build notice overrides feed/download status even when automatic
downloads are disabled. It disables future updater install-on-quit handoffs.
On macOS this cannot revoke a request already handed to Squirrel: ShipIt can
still replace the bundle on any process exit, without `quitAndInstall`.
The MacUpdater handoff and native checking/completion therefore latch a restart
blocker for this launch, including after later errors or discarded staging
metadata. Both external-build restart choices are blocked, and queued restarts
are cancelled before exit.
The dialog explains how to let the prepared update finish and then reinstall
the desired build. This deliberately avoids a second installer, signature
assumptions about local builds, or mutation of Squirrel's OS-owned cache.
The sidebar opens the existing restart dialog; an idle board receives one
automatic prompt per identity.
Only an explicit Restart now or Restart when idle request can relaunch.

For AppImage replacements, stable size/hash observations do not prove that the
copy is complete. Both restart choices remain blocked, including queued idle
restarts. The dialog asks the user to obtain a verified complete download before
quitting and opening it manually; this guard does not execute an unverified
replacement or attempt to infer completeness from a paused copy.

Main refreshes local session facts before relaunching. Active, approval,
provisioning, reviewing, unavailable and unknown states keep it waiting, as do
unsafe unsaved chat drafts. Stored idle activity is insufficient when status is
`no_signal` or the runtime is unreachable. Each live session's review API must
answer and all recorded review runs must be terminal; a display label for one
PR cannot hide another running review. Scheduled and retrying reviews remain
busy even without a running pass. The review API derives `autoReviewPending`
from every current PR head, the selected harness, and the engine's retry policy;
CI or another PR's display label cannot hide this work. Every non-archived Chat
reviewer is included even without a terminal handle. Its live conversation
controller must be settled and its turns terminal; stored hook activity is not
used for Chat reviewers. TUI activity must be idle or exited, and unreported
activity for another TUI reviewer remains busy.

Side-launch capture waits for recovery on the current daemon attachment. Main
captures before and after the worker probes and rejects a changed attachment.
Any retained side chat keeps both restart choices waiting, including a completed
conversation or an idle side with a draft: quitting retires their launch-scoped
state. The user must finish reading, save any text, and close those sides before
restarting. Unavailable recovery or capture facts and locally pending side-close
state also keep the gate closed. The user can cancel the in-memory queue.
The normal quit path preserves agents; no harness stop/update/resume operation
is added. No restart intent, preference or dismissal marker is persisted. A fresh
process pins the newly installed archive and has no inherited mismatch or prompt.

Before opening a packaged window, main copies the built renderer and preloads
to its private `~/.ao/electron/running-renderer` cache, checking the physical
archive identity before and after capture. The protocol serves that snapshot,
so unloaded session/Settings chunks and reloads remain usable while work becomes
idle, without mixing in replacement resources. Clean quit removes the cache;
the next launch replaces any cache left by a crash. A capture that cannot prove
its source identity fails closed. AppImage uses its existing immutable mount
without comparing each resource request against the replaced outer file.
A baked renderer/main identity handshake also fences a cached renderer from
another main build.

## Host-only updates

A host release swap alone cannot refresh desktop JavaScript. Startup/reattach
compares VCS identities for bundled daemons and reports a different-build error;
an explicit custom `AO_DAEMON_COMMAND` retains its existing attachment policy.
An attached child is not continuously compared to desktop build identity.
Restarting an unchanged desktop cannot make it match a separately upgraded host,
so host-only swaps deliberately do not trigger a desktop restart loop. Updating
the desktop bundle activates the archive guard independently of daemon health.
