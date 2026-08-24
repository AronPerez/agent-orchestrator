## What

`ao session kill`, `ao session cleanup` and `ao project rm` hit the right daemon, but say nothing about which one. Their prompts and success lines — plus the `registered project … at …` echo — now name the daemon when one was given.

## Why

Part of the CLI half of the remote-hosts series proposed in #RFC, on top of the `--url` flag. "Remove project `"api"`? Type the project id to confirm:" and "Clean 2 terminated sessions across all projects?" both beg the question "on whose machine?", and neither a session id nor a project id is host-qualified — the same id can exist on two daemons. For a destructive verb, being unable to tell where you are about to act is the defect.

## How

One helper, `resolvedBySuffix()`, returns `" on the remote daemon at <url>"` for a remote target and `""` for a local one. `confirmProjectRemoval` and `confirmSessionCleanup` become methods so the prompt can carry it; `session kill` (both the freed and workspace-preserved lines), `session cleanup` (the prompt, the dry-run line and the completion summary) and `project rm` carry it on output.

The `project add` path echo needed it most: for an absolute path the echoed string is byte-identical to what the operator typed, so it carried no information at all about which machine resolved it. That echo is the one moment a wrong host is still catchable.

Because the suffix is empty for a local daemon, local output is unchanged. The tests assert the local forms as **exact literals** rather than substrings, so a future change that appends anything to a local line fails here rather than silently drifting.

## Testing

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/`; `gofmt -l` clean. Four new tests: the helper itself (empty locally, names the daemon remotely), and the prompt-plus-output pairs for `project rm`, `session kill` and `session cleanup`, each covering the remote form and the exact local literal. Path-free by design so they assert identically on every runner OS.

No frontend file and no OpenAPI surface is touched, so the `frontend`, `renderer-smoke` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
