## What

A Host dropdown in Add-a-project lists the saved remote daemons beside
This Mac, each with its live reachability, and manages them in place:
add, edit and remove, with the connection password never leaving the
main process. Picking a remote host replaces the native folder dialog —
which would open on this machine — with an absolute path on that one,
and registers the project against that daemon over REST; the daemon owns
the verdict on its own filesystem, so a rejected path is reported in its
words rather than pre-judged here.

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change. It opens after the flag, the host primitives, the saved-host store, the loopback proxy and the per-host clients have all merged — it is the first PR in the series where the feature is reachable by a user, and still only with the flag on.

The picker and what picking does are one PR on purpose. Split apart, the intermediate state ships a Host dropdown where choosing another machine opens the **local** folder dialog and registers the project on the **local** daemon — a control that silently acts on the wrong host. There is no reviewable slice smaller than "pick a host, and have picking mean something".

## How

The dropdown is a popover rather than a Select because each row carries
Connect, Edit and Remove buttons: Radix's Select moves focus only between
options, so those buttons were mouse-only, and a listbox whose children
are buttons is not a listbox a screen reader can report. An unreachable
host stays focusable but unselectable, and says which of the four ways it
failed in text rather than colour.

Address entry normalises before it parses, not after: `new URL("workbox:3011")` reads `workbox` as a scheme, so a bare `host:port` never fails in a way that can be detected afterwards. Getting that wrong is what made a typo surface as "could not reach that host" and send someone to debug their network. A typo and a silent host now get different sentences. A URL carrying userinfo is refused with the same words the CLI uses.

Editing never round-trips a credential: the dialog is handed a `{label, url}` view, the only shape main gives the renderer, and a blank password field means "keep the saved one" rather than "clear it".

`ProjectSourcePickerView` gains one optional `hostRow` slot (3 lines in `packages/product-ui`); with the flag off it receives `undefined` and renders exactly the tree it does today.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer/hooks/useRemoteHosts.test.tsx src/renderer/components/HostSelect.test.tsx src/renderer/components/AddRemoteHostDialog.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx src/renderer/components/CreateProjectFlow.test.tsx src/renderer/test/fake-daemon.test.ts src/renderer/i18n/instance.test.ts` — 7 files, 72 tests. The existing `CreateProjectFlow.test.tsx` is in that list deliberately: it renders with the flag at its default `false` and is the flag-off regression check. Also `npm --prefix packages/product-ui test` — 10 files, 64 tests.

No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected.

If 14 files is too much for one review, add/edit and remove split cleanly along `AddRemoteHostDialog` / `ConfirmDialog` — happy to resubmit as two.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
