## What

HostId, Ref = {host, id}, LOCAL_HOST and a composite refKey. Local is a
host like any other so no code path special-cases "is this remote?";
a project id is filepath.Base(path) on every machine, so a bare id is
never enough to act on and Ref qualifies it at the addressing boundary.
No importer yet.

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change.

## How

See the commit body.

## Testing

`cd frontend && npm run typecheck && npx vitest run src/renderer/lib/hosts.test.ts` — counts as in the table in `docs/upstreaming-stack-status.md`. No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
