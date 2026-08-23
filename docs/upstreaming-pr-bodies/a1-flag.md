## What

A Remote hosts switch directly below Developer Mode, modelled on it:
remoteHosts in ui-store, persisted at ao.remoteHosts, default off.
Nothing reads the flag yet; the remote-host feature lands behind it in
the following PRs, so with it off there is no behaviour change at all.

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change.

## How

See the commit body.

## Testing

`cd frontend && npm run typecheck && npx vitest run src/renderer/stores/ui-store.test.ts src/renderer/components/GlobalSettingsForm.test.tsx` — counts as in the table in `docs/upstreaming-stack-status.md`. No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
