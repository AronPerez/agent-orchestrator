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
check rejects truncated copies; two matching observations confirm a replacement.

Main watches the resources directory and, on macOS, the enclosing bundle's
parent. A 30-second timer reattaches watches and checks again; focus and resume
also check. Each check has a two-second follow-up. AppImage uses its stable
outer file rather than the old mounted archive. Unpackaged development keeps
its existing reload workflow.

An installed-build notice overrides feed/download status even when automatic
downloads are disabled. It disables updater install-on-quit so an older staged
download cannot overwrite the external install. The sidebar opens the existing
restart dialog; an idle board receives one automatic prompt per identity.
Only an explicit Restart now or Restart when idle request can relaunch.

Main refreshes local session facts before relaunching. Active, approval,
provisioning, reviewing, unavailable and unknown states keep it waiting, as do
unsafe unsaved chat drafts. The user can cancel the in-memory queue. The normal
quit path preserves agents; no harness stop/update/resume operation is added.
No new file, preference or dismissal marker is persisted. A fresh process pins
the newly installed archive and has no inherited mismatch or prompt.

The packaged renderer protocol checks the archive before and after reading
each resource. It refuses swapped resources, including reloads and lazy chunks,
instead of mixing new renderer code with old main code. A baked renderer/main
identity handshake also fences a cached renderer from another main build.

## Host-only updates

A host release swap alone cannot refresh desktop JavaScript. Startup/reattach
compares VCS identities for bundled daemons and reports a different-build error;
an explicit custom `AO_DAEMON_COMMAND` retains its existing attachment policy.
An attached child is not continuously compared to desktop build identity.
Restarting an unchanged desktop cannot make it match a separately upgraded host,
so host-only swaps deliberately do not trigger a desktop restart loop. Updating
the desktop bundle activates the archive guard independently of daemon health.
