## What

clientFor(host) binds an openapi-fetch client to each connected host's
proxy base (local keeps reading the live daemon base). initHosts() runs
after first paint and connects every saved host — but only while the
Remote hosts flag is on: off means the saved-host file is never read and
no proxy is started, and turning the flag off tears every remote proxy
down without a restart. With the flag off this change is inert.

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change. Opens last — it builds on the flag, the host primitives and the proxy, all merged by then.

## How

See the commit body.

## Testing

`cd frontend && npm run typecheck && npx vitest run src/renderer/lib/host-clients.test.ts src/renderer/lib/active-host.test.ts` — counts as in the table in `docs/upstreaming-stack-status.md`. No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
