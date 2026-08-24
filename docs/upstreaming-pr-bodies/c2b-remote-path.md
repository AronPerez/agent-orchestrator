## What

With `--url`, the daemon resolves `--path` against **its** filesystem, and every implicit project signal (`AO_PROJECT_ID`, `AO_SESSION_ID`, the current directory) is matched against **its** projects. Both are now judged by the remote host's rules, and the local `gh` fallback no longer runs against a remote project.

## Why

Part of the CLI half of the remote-hosts series proposed in #RFC, on top of the `--url` flag. Each of these paths reads a signal that describes the machine running the CLI and hands it to a daemon that will interpret it against a different machine — silently, with output that looks like success.

## How

**`--path`.** `ao project add --path '~/repo' --url <remote>` registers the remote host's `~/repo` and never consults the operator's. Host-relative paths (`~…`, `./…`, bare names) are refused for a remote target with exit 2, naming the URL. Absolute paths stay allowed — they are meaningful on that host, and refusing them would make a remote target useless. Absoluteness is judged for **any** host rather than for the OS running the CLI: `filepath.IsAbs` calls `/srv/repo` relative on Windows, so a Windows operator could not register a project on a Linux daemon. POSIX absolute, Windows drive-absolute (`C:\…`, `C:/…`) and UNC (`\\server\share`) are accepted; drive-relative `C:foo` and current-drive `\foo` are not, because both are host-relative even on Windows.

**Implicit project.** `ao spawn --url <remote>` run inside any AO session inherits an `AO_PROJECT_ID` the operator never typed and spawns against whatever project on the remote host happens to share that id; cwd matching picks a remote project whenever the two machines' layouts coincide. A session started on the wrong machine cannot be caught from the output, which names only the session it created. `--project` is now required for a remote target: it is the one input that means the same thing on both hosts, and the refusal points at `ao project ls --url <same URL>` to find it.

**PR refs.** Resolving a numeric PR ref falls back to running `gh repo view` in the project's checkout when the project record has no repo URL. With a remote target that path came from the remote daemon and `gh` runs here — so either the path does not exist locally and the operator is told "gh not available", which is a misdiagnosis, or it does exist (two machines with the same checkout layout is the normal case) and the PR is resolved from this machine's checkout and this machine's `gh` credentials, then sent to the remote daemon as if it had come from there. It now refuses and asks for the full PR URL.

**`project ls` gains a `PATH` column**, and `--json` a `path` field. The daemon has always returned it; the CLI dropped it on decode, so neither could answer "where does this project actually live" — which is the whole question once the answering daemon is on another machine. `PATH` goes last: it is the widest column and the only one whose meaning depends on which daemon answered.

Every check returns `nil` on its first line without a remote target, so local behaviour is unchanged.

## Testing

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/`; `gofmt -l` clean. Eight new tests: the path refusal across every host-relative and absolute form on every runner OS (deliberately path-free — no `filepath`, no `t.Chdir`), the implicit-project refusal for each local signal plus the explicit-`--project` escape and the unchanged local case, `ao spawn --url` refusing *before* any request (empty request log), the `PATH` column in both table and `--json`, and four `resolvePRRef` cases: refuse for a remote project, accept a full URL, the same through `ao session claim-pr`, and the local `gh` fallback still working.

No frontend file and no OpenAPI surface is touched, so the `frontend`, `renderer-smoke` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
