## What

The desktop app reads and writes the CLI's ~/.ao/remotes.json (mode 0600,
refused if looser; win32 exempt because Node reports 0o666 there), probes
a host through /healthz with the saved connection password as a Bearer
token, and exposes list/add/update/remove/probe/request over IPC. Only
{label, url} ever crosses to the renderer; a request path that would
redirect the credential off-host is refused before anything is sent.

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change.

## How

Nothing in the renderer calls this yet; it lands dark.

## Testing

`cd frontend && npm run typecheck && npx vitest run src/main/remotes-store.test.ts src/main/remote-request.test.ts src/main/remotes-ipc.test.ts src/main/remotes-main.test.ts` — counts as in the table in `docs/upstreaming-stack-status.md`. No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
