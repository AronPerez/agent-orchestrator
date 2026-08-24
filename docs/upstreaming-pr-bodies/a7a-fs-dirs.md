## What

A client connected to a daemon on another machine has no way to see that
machine's filesystem, so a project path there has to be typed blind. This
answers with the subdirectories of one absolute path: names, paths, and
whether each carries a `.git` entry (a directory for a clone, a file for a
worktree checkout). Dotted names are skipped, files are not listed, and a
listing is capped at 500 entries with `truncated` set.

## Why

Part of the remote-hosts series proposed in #RFC, and the only PR in it that touches Go. It has no dependency on the rest of the series and can be reviewed and merged on its own, in any order relative to the others.

**Is this an escalation?** No. It sits behind the same connection credential that already authorises spawning an agent — that is, a shell — on the host, so it grants no reach that credential did not already have. It reads directory names and nothing else: no file contents, no sizes, no timestamps, no dotted names. The alternative is what the client does without it, which is ask people to type an absolute path for a filesystem they cannot see.

## How

The daemon judges the path by its own OS's rules — a remote client cannot know what a valid absolute path looks like over there, so a relative path is refused here (`FS_PATH_NOT_ABSOLUTE`) rather than pre-judged client-side. `ENOENT`, `EACCES` and `ENOTDIR` are separated into 404 / 403 / 400 so a caller can tell "no such folder" from "may not read it" from "that is a file".

The route is declared in `specgen` beside the others and the spec and `schema.ts` are regenerated, so `TestRouteSpecParity` and `api-drift` both hold; regeneration is idempotent.

The LAN test pins both halves of its policy: credential-gated like every other data route, and specifically **not** on `lanControlBlockedPrefixes`. That second half is the one worth having — a `ROUTE_LOOPBACK_ONLY` answer would kill remote browsing silently, and no loopback-side test could catch it. The assertion was falsified before it was trusted: adding `/api/v1/fs` to the blocked prefixes makes it fail.

## Testing

`cd backend && go build ./... && go vet ./internal/httpd/... && go test ./internal/httpd/...` — all green, including `TestListDirs*` ×4 and the LAN policy assertion. `npm run api` produces no diff (`api-drift`).

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
