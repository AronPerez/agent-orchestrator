# Releasing this fork

This fork (`AronPerez/agent-orchestrator`) publishes its own GitHub Releases for
macOS and Linux. People download and set them up themselves. The desktop app's
updater follows **only** this fork's releases.

- Workflow: [`.github/workflows/fork-release.yml`](.github/workflows/fork-release.yml)
- Releases are cut from **`develop`**. `main` mirrors upstream and is never released.
- A tag push builds everything and creates a **draft**. A person reviews the
  draft and publishes it. The workflow never publishes, pushes tags, or edits
  or deletes a release.

## Versions and tags

Tags are `vX.Y.Z-fork.N`:

- `X.Y.Z` is `frontend/package.json` on the tagged `develop` commit, which is
  the upstream version the fork has merged. The workflow refuses a tag whose
  `X.Y.Z` differs.
- `N` starts at `1` for each base version and goes up by one per release:
  `v0.15.3-fork.1`, `v0.15.3-fork.2`, then `v0.15.4-fork.1` after an upstream
  sync bumps `package.json`.

The version is stamped from the tag at build time; never edit `package.json`
to make a release. Fork tags never collide with upstream's `vX.Y.Z` tags, and
they sort correctly across syncs (`0.15.3-fork.2 < 0.15.4-fork.1`). The app
treats `-fork.N` builds as the Stable channel.

## One-time setup: Apple signing secrets

macOS releases are always signed with an Apple Developer ID and notarized. A
tag build **fails** if any of these six repository secrets is missing; it never
ships an unsigned Mac build. You need a paid
[Apple Developer Program](https://developer.apple.com/programs/) membership
(USD 99 per year).

Add secrets with the GitHub CLI from a terminal on your Mac. `gh secret set`
reads the value from stdin or prompts for it, so the value never lands in a
file or your shell history. Or use the repository's
**Settings → Secrets and variables → Actions → New repository secret** page.
Delete the temporary `.p12` and `.p8` copies afterwards, or keep them only in
your password manager.

### 1. `CSC_LINK`: the Developer ID certificate (.p12), base64

1. On your Mac, open **Keychain Access → Certificate Assistant → Request a
   Certificate From a Certificate Authority…**. Enter your email, choose
   **Saved to disk**, and save the `.certSigningRequest` file.
2. Open [developer.apple.com → Certificates](https://developer.apple.com/account/resources/certificates/list)
   and click **+**. Choose **Developer ID Application** (G2 Sub-CA), upload
   the request, download the `.cer`, and double-click it to add it to your
   login keychain.
3. In Keychain Access → **My Certificates**, select
   **Developer ID Application: <Your Name> (<TEAMID>)** (it shows a private
   key under it). Choose **File → Export Items…**, save as
   `DeveloperID.p12`, and set a strong export password.
4. Store it:

   ```sh
   base64 -i DeveloperID.p12 | gh secret set CSC_LINK -R AronPerez/agent-orchestrator
   ```

### 2. `CSC_KEY_PASSWORD`: the .p12 export password

```sh
gh secret set CSC_KEY_PASSWORD -R AronPerez/agent-orchestrator   # paste the password at the prompt
```

### 3. `APPLE_SIGNING_IDENTITY`: the certificate's full name

```sh
security find-identity -v -p codesigning
# copy the quoted name, e.g. Developer ID Application: Jane Doe (AB12CD34EF)
gh secret set APPLE_SIGNING_IDENTITY -R AronPerez/agent-orchestrator   # paste it at the prompt
```

### 4–6. App Store Connect API key for notarization

1. Open [App Store Connect → Users and Access → Integrations → App Store Connect API](https://appstoreconnect.apple.com/access/integrations/api),
   choose **Team Keys**, and generate a key with the **Developer** role.
2. Download `AuthKey_<KEYID>.p8`. Apple lets you download it **once**.
3. Note the **Key ID** (in the key's row) and the **Issuer ID** (above the list).
4. Store them:

   ```sh
   base64 -i AuthKey_<KEYID>.p8 | gh secret set APPLE_API_KEY_BASE64 -R AronPerez/agent-orchestrator
   gh secret set APPLE_API_KEY_ID -R AronPerez/agent-orchestrator   # paste the Key ID
   gh secret set APPLE_API_ISSUER -R AronPerez/agent-orchestrator   # paste the Issuer ID (a UUID)
   ```

Check that all six exist (values are never shown):

```sh
gh secret list -R AronPerez/agent-orchestrator
```

### Optional repository variables

| Variable | Default when unset |
|---|---|
| `VITE_WORKOS_CLIENT_ID` | The WorkOS client ID the app already ships (`frontend/src/main/cloud-auth.ts`). It is a public OAuth client ID, not a secret. |
| `VITE_AO_TEAMS` | Unset gives stable builds no Teams; `on` or `off` forces it. |

## Cutting a release

Only a maintainer pushes release tags.

1. **Check `develop`.** CI is green and the changes you want are merged.
2. **Pick the version.** Base = `jq -r .version frontend/package.json` on
   `origin/develop`. `N` = one more than the newest `v<base>-fork.*` tag, or `1`.
3. **Dry run (recommended).** It builds and signs everything, then uploads
   the files as run artifacts without creating a release:

   ```sh
   gh workflow run fork-release.yml -R AronPerez/agent-orchestrator \
     -f ref=develop -f version=0.15.3-fork.1
   ```

   Before the Apple secrets exist, add `-f skip-mac-signing=true`. That builds
   macOS unsigned, and only a dry run may do so.
4. **Tag and push:**

   ```sh
   git fetch origin develop
   git tag -a v0.15.3-fork.1 origin/develop -m "Orchestrator v0.15.3-fork.1"
   git push origin v0.15.3-fork.1
   ```

   The workflow checks the version, that the commit is on `develop`, and that
   no release exists for the tag yet. It then builds, signs and notarizes, and
   creates a **draft** release.
5. **Review the draft** on the Releases page:
   - Assets: four macOS zips (versioned and stable-named) and two dmgs, the Linux AppImage (versioned and
     stable-named), its `.blockmap`, `.deb`, `.rpm`, `ao-host-linux-x64.tar.gz`,
     `ao-host-linux-arm64.tar.gz`, `latest-mac.yml`, `latest-linux.yml` and
     `SHA256SUMS`.
   - Notes: generated from the PRs merged since the previous published fork
     release, under an install header.
6. **Publish.** Edit the draft, keep **Set as the latest release** checked, and
   click **Publish release**. Or run
   `gh release edit v0.15.3-fork.1 -R AronPerez/agent-orchestrator --draft=false --latest`.
   Installed apps see the update on their next check, and `bootstrap-fork-host.sh`
   installs it by default.

**Redoing a release.** If a draft is wrong, delete the draft
(`gh release delete <tag>`) and fix `develop`. Then cut the **next** number
(`-fork.N+1`) instead of moving a tag that may already have been fetched.

## Release assets

| Asset | For |
|---|---|
| `agent-orchestrator-darwin-arm64.dmg`, `agent-orchestrator-darwin-x64.dmg` | macOS first install (Apple silicon, Intel) |
| `Orchestrator-darwin-{arm64,x64}-<version>.zip` | macOS in-app updates (read through `latest-mac.yml`) |
| `agent-orchestrator-darwin-{arm64,x64}.zip` | `ao start` and `setup-self-hosted.sh` on macOS |
| `agent-orchestrator-linux-x64.AppImage` | Linux desktop, any distro |
| `Orchestrator-<version>.AppImage` (+ `.blockmap`) | Linux in-app updates (read through `latest-linux.yml`) |
| `agent-orchestrator-linux-x64.deb`, `agent-orchestrator-linux-x64.rpm` | Debian/Ubuntu, Fedora/RHEL/openSUSE |
| `ao-host-linux-x64.tar.gz`, `ao-host-linux-arm64.tar.gz` | Headless self-hosted hosts (daemon, ACP runtime, tmux) |
| `latest-mac.yml`, `latest-linux.yml` | Update feeds |
| `SHA256SUMS` | Checksums of every other asset |

There is no arm64 Linux desktop build, and no Windows build for fork releases.

## Installing

The fork app uses the same app identity and the same `~/.ao` data directory as
upstream Orchestrator. **Installing it replaces an upstream install.** Going
back to an upstream build later may mean running older code against data this
build has already migrated.

**macOS.** Open the dmg for your chip and drag **Agent Orchestrator** to
Applications. The build is signed and notarized, so it opens without
Gatekeeper warnings and updates itself.

**Linux AppImage.**

```sh
chmod +x agent-orchestrator-linux-x64.AppImage
./agent-orchestrator-linux-x64.AppImage
```

The AppImage updates itself.

**Debian or Ubuntu:** `sudo apt install ./agent-orchestrator-linux-x64.deb`.
**Fedora or RHEL:** `sudo dnf install ./agent-orchestrator-linux-x64.rpm`.
These packages do not update themselves: install each new release the same way.

**Headless host (Debian/Ubuntu, x64 or arm64).** Run as the host user, not root:

```sh
curl -fsSL https://raw.githubusercontent.com/AronPerez/agent-orchestrator/develop/scripts/bootstrap-fork-host.sh | bash
# a specific release:                    ... | bash -s -- --release v0.15.3-fork.1
# build develop from source instead:     ... | bash -s -- --ref develop
```

It downloads `ao-host-linux-<arch>.tar.gz` from the release and checks it
against `SHA256SUMS` and GitHub's asset digest. It then installs the host with
`setup-self-hosted.sh` from the same tag, as a systemd user service. Re-run it
to upgrade.

### Verifying a download

```sh
# Linux
sha256sum --check --ignore-missing SHA256SUMS
# macOS
grep ' agent-orchestrator-darwin-arm64.dmg$' SHA256SUMS | shasum -a 256 -c -
spctl --assess -vv "/Applications/Agent Orchestrator.app"   # expect: source=Notarized Developer ID
```

## How the updater stays on the fork

- Every build bakes `AronPerez/agent-orchestrator` into `app-update.yml`
  (`AO_RELEASE_REPO`). The workflow fails if the baked repo is anything else.
- Stable reads the fork's latest published release and its
  `latest-mac.yml` or `latest-linux.yml`. Drafts are invisible until published.
- `ao start` and `scripts/setup-self-hosted.sh` download from this fork's
  releases (`setup-self-hosted.sh` honors `AO_RELEASE_REPO=owner/repo`). Before
  this, a bare `setup-self-hosted.sh` installed upstream's latest host build.
- The minimum-version check reads
  [`desktop-version-floor.json`](desktop-version-floor.json) from this fork's
  `develop`, so upstream cannot force-quit fork installs. Set `latest` to
  nudge users to update. Set `min` only to **block** every older build: they
  get a forced-quit dialog.
- The fork publishes no `nightly` or `pr<N>` feeds. A fork install set to
  Nightly finds no update; it never falls back to upstream.

## Troubleshooting

| Error | Fix |
|---|---|
| `Missing repository secrets for macOS signing: …` | Add the named secrets (above). Only a dry run may use `skip-mac-signing`. |
| `Version base X.Y.Z does not match frontend/package.json` | Tag `v<package.json version>-fork.N`. |
| `… is not on develop` | Tag a commit on `origin/develop`. |
| `A release for <tag> already exists` | Delete the draft, or cut the next `-fork.N`. |
| Notarization step fails | Check that the API key has the Developer role and that `APPLE_API_ISSUER` is the Issuer ID, not the Key ID. |
