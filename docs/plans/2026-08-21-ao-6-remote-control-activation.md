# Spec: Streamline remote control activation (AO-6)

**Linear:** [AO-6 — Streamline remote control activation (replace manual `/rc`)](https://linear.app/starstruck/issue/AO-6/streamline-remote-control-activation-replace-manual-rc)
**Status:** proposed — no implementation in this document
**Related:** [`docs/remote-sessions-edd.md`](../remote-sessions-edd.md), [`docs/architecture.md`](../architecture.md)

---

## Problem

To drive a laptop AO session from a phone (the Claude iOS app / claude.ai), the
user must type `/rc` into that session's TUI. Every session. Every time.

That is worse than it sounds:

- It is **per session**, not per machine. Ten AO sessions means ten `/rc`s.
- It must be done **at the laptop**, before leaving — which is precisely the
  situation remote control exists to remove. Forget it, and the session is
  unreachable from the phone until you are back at the keyboard.
- It does not survive a **restore**. AO restores sessions on app launch
  (`POST /sessions/{id}/restore`, `POST /sessions/{id}/resume-agent`), and a
  restored Claude process is a fresh process with remote control off.
- The window where it matters — an agent that hits a permission prompt or
  finishes while you are away — is exactly the window where nobody typed `/rc`.

AO already owns the launch argv for every session it spawns
(`ports.Agent.GetLaunchCommand` → `agentruntime.BuildLaunchCommand`). It simply
does not use that ownership here.

### The fact that makes this cheap

Claude Code exposes remote control as a launch flag, not only as a slash command
(verified against `claude --help`, Claude Code **2.1.239**, 2026-08-21):

```
--remote-control [name]                    Start an interactive session with Remote
                                           Control enabled (optionally named)
--remote-control-session-name-prefix <p>   Prefix for auto-generated Remote Control
                                           session names (default: hostname)
```

So the manual step is not a missing capability. It is a flag AO is not passing.

### Check this before writing any code

If Claude Code also honours a `settings.json` key or an environment variable for
remote control, the whole feature collapses into documentation plus at most one
line in `ProjectConfig.Env` (which already forwards env vars into worker
runtimes). `claude --help` does not advertise one and no such key was verified,
but the flag surface changes often enough that the first task below is to check
rather than assume. Everything after that assumes "flag only".

---

## Proposed design

Three independent layers. Each ships alone and is useful alone; layer 1 is the
whole feature for new sessions, layer 2 stops it from silently decaying, layer 3
covers sessions that already exist.

### Layer 1 — Pass the flag at launch (opt-in, per project)

New project config block, following the `trackerIntake` / `containerReap`
precedent in `domain.ProjectConfig`:

```jsonc
{
  "remoteControl": {
    "enabled": true,          // default false
    "namePrefix": "laptop"    // optional; empty = Claude Code's default (hostname)
  }
}
```

When enabled, the Claude Code adapter emits `--remote-control <name>` where
`<name>` is the **AO session id**, so the session list on the phone matches the
session list on the AO board. No new naming scheme, no mapping table.

**The flag is carried by an existing seam.** `agentruntime.LaunchConfig` and
`RestoreConfig` already have `ProviderArgs` — "trusted host-owned flags inserted
before model and prompt arguments" — and `buildClaudeLaunch` / `buildClaudeRestore`
already append them (`backend/pkg/agentruntime/command.go:224,243`). Codex uses
this today; Claude Code passes nothing. So `agentruntime` needs **no change at
all**: the adapter computes `[]string{"--remote-control", sessionID}` and hands
it over.

The enable/disable decision travels on `ports.LaunchConfig` / `ports.RestoreConfig`
as two new fields (`RemoteControl bool`, `RemoteControlName string`), populated by
the session manager from the resolved project config. That mirrors how
`Permissions`, `AllowedTools`, and `WorkspacePath` already reach adapters:
host-decided facts on the launch config, not on the provider-neutral
`domain.AgentConfig`. Adapters that do not implement remote control ignore the
fields, so nothing else in the 23-adapter registry changes.

### Layer 2 — Restore parity

`GetRestoreCommand` must set the same `ProviderArgs`. Without this the feature
works until the first app restart and then quietly stops, which is a worse bug
than the one being fixed: the user believes remote control is on, and it is not.

Scope note: `session_mode = chat` sessions have no TUI and no argv, so remote
control does not apply to them. A TUI→Chat interface transition drops it; the
Chat→TUI direction re-establishes it through the same restore path. This must be
stated in the setting's help text rather than discovered.

### Layer 3 — Retrofit a running session

For a session that is already up without the flag, AO can type `/rc` so the user
does not have to. `POST /sessions/{id}/send` already writes into a live pane
through `sessionguard` under the `Deliver` policy, which permits a write to a
session waiting at its prompt and refuses one to a session `blocked` on a
permission decision. Surface it as one action ("Enable remote control") on the
session, available from desktop **and** from AO's own mobile app — so the
remaining manual step is a tap from the phone rather than a trip to the laptop.

**This layer has a correctness prerequisite.** `SessionsController.send` responds
`{"ok": true}` unconditionally (`backend/internal/httpd/controllers/sessions.go:1299`),
because `sessionguard.Guard.Send` folds a suppressed outcome into a nil error.
An action built naively on `/send` would report "remote control enabled" for a
write the guard refused. The action must report the guard's real `Outcome`
(`Sent` vs `SuppressedAwaitingUser` / `SuppressedBusy` / …) or it lies at exactly
the moment the user is about to walk away from the machine.

### Explicitly not doing

- **A global "remote control everything" setting.** `service/settings` is the
  natural home and it is one field away — but the per-project toggle is the
  precedent, gets UI for free, and covers the stated need. Add the global
  default when someone has more than a handful of projects and says so.
- **A per-spawn override on `ports.SpawnConfig`.** Speculative until a caller
  wants it.
- **A provider-neutral remote-control abstraction.** One harness implements this
  today. An interface with one implementation is a cost with no payer; the
  `ports.LaunchConfig` fields already give any future adapter somewhere to look.
- **Auto-enabling by default.** See Risks — this changes who can reach the
  session.

---

## Touched components

| Area | File | Change |
| --- | --- | --- |
| Domain config | `backend/internal/domain/projectconfig.go` | `RemoteControlConfig` block + `Validate()` |
| Agent port | `backend/internal/ports/agent.go` | `RemoteControl`, `RemoteControlName` on `LaunchConfig` and `RestoreConfig` |
| Claude adapter | `backend/internal/adapters/agent/claudecode/claudecode.go` | Set `ProviderArgs` in `GetLaunchCommand` **and** `GetRestoreCommand` |
| Launch builder | `backend/pkg/agentruntime/command.go` | **No change** — `ProviderArgs` already appended in both Claude builders |
| Session manager | `backend/internal/session_manager/manager.go` | Populate the launch/restore fields from resolved project config |
| API contract | `backend/internal/httpd/apispec`, `frontend/src/api/schema.ts` | Regenerate (`npm run api`); enforced by the `api-drift` CI job |
| Settings UI | `frontend/src/renderer/components/ProjectSettingsForm.tsx`, `frontend/src/renderer/i18n/*.json` | Toggle + prefix field + help text |
| Session action | renderer session actions, `packages/mobile` | "Enable remote control" → `/send` with honest outcome reporting |
| Guard outcome | `backend/internal/httpd/controllers/sessions.go` | Return the guard `Outcome` on the path layer 3 uses |
| Docs | `frontend/src/landing/content/docs/configuration/projects.mdx` | Document the setting and its trust boundary |

---

## Risks

**Remote control widens who can reach the session — state it in the UI.**
An AO worker session frequently runs with `--dangerously-skip-permissions`
(AO's bypass permission mode). Turning on remote control makes that session
drivable from any device signed into the same Claude account. This is a
deliberate capability, not a bug, but it is a genuine change to the trust
boundary described in the remote-sessions EDD, where reach was previously
bounded by loopback plus a credential-gated LAN listener. Consequences: default
**off**, an explicit opt-in per project, and setting copy that says what it
grants rather than calling it a convenience toggle.

**The flag is an external contract.** `--remote-control` belongs to Claude Code,
not to AO. If it is renamed, every spawn on an enabled project fails at process
start. Mitigations, cheapest first: keep the feature opt-in (blast radius is
projects that asked for it); pin the observed flag and version in the adapter's
doc comment; only if this actually bites, add a cached capability probe next to
the existing `authprobe` pattern rather than shelling out per spawn.

**Silent decay through restore.** Covered by layer 2, and it is the reason layer
2 is not optional. A test that asserts the restore argv contains the flag is the
cheapest guard against a regression that is invisible until someone is away from
their desk.

**Name collisions across machines.** With multi-host federation an AO session id
is unique per daemon, not globally — `agent-orchestrator-80` can exist on two
machines. `--remote-control-session-name-prefix` (default: hostname) already
disambiguates; the config exposes it so a user with two laptops can label them.

**Unverified auth requirement.** Whether remote control works under every Claude
Code auth mode (OAuth vs `ANTHROPIC_API_KEY` vs Bedrock/Vertex) was not verified.
If it requires an account-backed session, an enabled project on an API-key setup
may fail at spawn. Verify before layer 1 ships; if confirmed, the setting needs a
preflight or a documented prerequisite.

**Layer 3 can report success for a write that never landed.** Verified above;
addressed by returning the guard outcome. Without that fix, layer 3 should not
ship at all.

---

## Suggested PR breakdown

Each PR is independently reviewable and independently revertable. PRs 1–3 are
sequential; 4–6 can be parallelized once 3 lands.

| # | PR | Size | Notes |
| --- | --- | --- | --- |
| 0 | **Spike (no PR):** confirm whether a `settings.json` key or env var exists for remote control, and which auth modes support it | XS | If a settings key exists, PRs 1–4 collapse into PR 6 |
| 1 | `RemoteControlConfig` in `domain.ProjectConfig` + validation + tests | S | No behaviour change; config parses and round-trips |
| 2 | Claude Code adapter emits `--remote-control <session-id>` via `ProviderArgs` on launch **and** restore | S | Pure argv tests, mirroring `TestGetLaunchCommandMapsPermissionModes` |
| 3 | Session manager plumbing + OpenAPI/schema regen | S | `npm run api`; `api-drift` CI must be green |
| 4 | `ProjectSettingsForm` toggle + prefix + i18n + trust-boundary copy | S | Default off |
| 5 | "Enable remote control" session action (desktop + mobile), **including** returning the real `sessionguard` outcome | M | Blocked on the outcome fix; do not ship the action without it |
| 6 | Docs: `projects.mdx` setting reference, restore/Chat-mode caveats, what remote control grants | S | |

**Verification per PR:** `go test ./...` and `go test -race` for backend PRs;
`npm run test` + typecheck for frontend PRs; and for PR 2 specifically, one
manual end-to-end check — spawn a session on an enabled project, confirm it
appears in the Claude iOS app without anyone typing `/rc`, restart AO, confirm
the restored session still appears.
