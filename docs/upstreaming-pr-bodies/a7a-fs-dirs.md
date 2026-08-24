## Ticket

No upstream issue yet. Design note: [remote hosts RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md).

## Problem

A client connected to a daemon on another machine cannot see that machine's filesystem, so an absolute project path over there has to be typed blind — with no way to tell a typo from a folder that is really missing, and no way to know whether a directory is already a checkout. Anyone adding a project on a second machine is guessing.

## Solution

A read-only `GET /api/v1/fs/dirs` answers with the subdirectories of one absolute path: names, paths, and whether each carries a `.git` entry (a directory for a clone, a file for a worktree checkout). Dotted names are skipped, files are never listed, and a listing is capped at 500 entries with `truncated` set. It is additive and sits behind the connection credential that already exists.

## How Has This Been Tested?

`cd backend && go build ./... && go vet ./... && go test ./...` on the current `main` (`c9a0adb2`): 158 packages ok, 0 failures, `gofmt -l` empty. That includes `TestListDirs` x4 and the LAN policy assertion. Regeneration is idempotent on this base: `go generate ./internal/httpd/apispec/...` followed by `npm run api:ts` leaves the working tree clean, so `TestRouteSpecParity` and the `api-drift` job both hold.

## Artifacts (if appropriate):

No renderable surface: Go plus two generated API files. The only `frontend/src` change is the regenerated `api/schema.ts`.

## Implementation notes

**Is this an escalation?** No. It sits behind the same connection credential that already authorises spawning an agent — that is, a shell — on the host, so it grants no reach that credential did not already have. It reads directory names and nothing else: no file contents, no sizes, no timestamps, no dotted names.

The daemon judges the path by its own OS's rules. A remote client cannot know what a valid absolute path looks like over there, so a relative path is refused by the daemon (`FS_PATH_NOT_ABSOLUTE`) rather than pre-judged client-side. `ENOENT`, `EACCES` and `ENOTDIR` are separated into 404 / 403 / 400 so a caller can tell "no such folder" from "may not read it" from "that is a file".

The route is declared in `specgen` beside the others and the spec and `schema.ts` are regenerated rather than hand-written.

The LAN test pins both halves of the policy: credential-gated like every other data route, and specifically **not** on `lanControlBlockedPrefixes`. That second half is the one worth having — a `ROUTE_LOOPBACK_ONLY` answer would kill remote browsing silently, and no loopback-side test could catch it. The assertion was falsified before it was trusted: adding `/api/v1/fs` to the blocked prefixes makes it fail.

Follow-up, not included here: nothing calls this endpoint yet. The client that uses it is a separate PR in the same series.
