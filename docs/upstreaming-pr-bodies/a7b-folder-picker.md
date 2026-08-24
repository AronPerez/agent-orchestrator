## What

Browse beside the remote path field walks the selected host's directories
over `GET /api/v1/fs/dirs` and drops the chosen one into the field. Every
path decision stays with the daemon: this dialog never joins, normalises
or judges a path, because it may be looking at a different OS than the
one it runs on. A typed path still wins, and is still the way in when the
host will not list.

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change, and the dialog is unreachable until a remote host is selected. It opens last of this wave — it needs both the host UI and the `fs/dirs` endpoint, and is an assist on top of a flow that already works with a typed path.

## How

A 200 is not proof of a listing — a daemon predating the endpoint answers
unknown routes with an HTML page from its web-UI catch-all — so the body
is shape-checked once at the parse boundary and an unreadable answer is
reported as a version gap, not as an empty folder. Casting it instead put
`undefined.map` on a render path and took the window down.

A refused directory keeps the last good listing on screen, so a dead end is not a dead dialog. Stepping into a folder replaces every row, which destroys whatever row had focus, so focus is moved into the new listing — but only after a step, never on first open, where Radix has already placed it correctly.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer/components/RemoteFolderPicker.test.tsx src/renderer/components/CreateProjectFlow.remote.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx src/renderer/components/CreateProjectFlow.test.tsx src/renderer/i18n/instance.test.ts` — 5 files, 45 tests.

No Go or OpenAPI surface is touched by this PR, so the `go` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
