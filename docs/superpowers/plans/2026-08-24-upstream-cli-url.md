# Upstream Remote Hosts — Plan C: CLI remote target (`--url` / `AO_URL`) (C1–C3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, on top of `upstream/main`, the five upstream-ready branches of Track C — the CLI's remote daemon target and the three refusal/labelling fixes it makes necessary, plus the daemon-side hunk that stops a LAN policy block reading as a missing route — each one green on the upstream base, each byte-identical to upstream with no `--url` and no `AO_URL`, plus the hand-off the human needs to open them upstream one at a time.

**Architecture:** Track C is Go-only and shares no code with Track A's desktop stack: it touches `backend/internal/cli/**` and one function in `backend/internal/httpd/lan_listener.go`. Every branch is a port of code that already ships on our `develop`, re-cut against `upstream/main`. `up-c1-url` is the root — it introduces the `--url` flag, the `remoteTarget` type and `remotes.json`; the other four are **independent siblings on `up-c1-url`**, because each fixes a different defect class that only exists once `--url` does, and none of them imports another's helper. That independence is not free: it required moving `resolvedBySuffix` out of fork #50 into C2c and splitting fork #61 across C2a and C2b (both justified in the task bodies). The off-state is not a feature flag but a construction: `resolveRemoteTarget("")` returns `nil` with no `AO_URL` set, and every guard added by C2a/C2b/C2c is `if c.remote == nil { return nil }` first. Nothing is opened against upstream: the final task writes the exact `gh pr create` commands for the human.

**Tech Stack:** Go 1.26 (`net/http`, `net/url`, `os`, `path/filepath`, `runtime`), cobra, chi, `httptest`, table-driven `testing` in the repo's existing `*_test.go` style. No npm, no frontend, no `openapi.yaml` surface — the `frontend` and `api-drift` CI jobs are untouched by every branch here.

**Spec:** `docs/upstreaming-remote-sessions.md` (merged, #123). This plan implements §2.5 (Track C: C1, C2 as three PRs, C3), §2.1's ground rules and the §3.3 scrub list. Plan 1 (`2026-08-23-upstream-remote-hosts-foundation.md`, A0–A5) is the structural template and runs in parallel; the two stacks share only `docs/upstreaming-stack-status.md`, which this plan's last task extends rather than rewrites. §2.5's optional follow-on (fork #49, `daemonmeta` build skew) is **out of scope**.

## Global Constraints

- **Do not push to, open PRs against, or comment on `Untrivial-ai/agent-orchestrator`.** `upstream` is fetch-only. All pushes go to `origin` (`AronPerez/agent-orchestrator`). The human opens upstream PRs from Task 7's hand-off. Do not merge any PR.
- **Every stack branch is based on `upstream/main`** (spec §2.1 rule 1), never on `develop`. Upstream squash-merges, so rebase the siblings with `git rebase --onto`, never by merging.
- **Off-state is a construction, not a flag** (spec §2.5): with no `--url` and no `AO_URL`, behaviour is byte-identical to upstream. Every branch carries at least one test that pins the local path unchanged, and the local paths' output strings are asserted as exact literals, not substrings.
- **No credential ever reaches an error message or a log line.** `normalizeRemoteURL` rejects userinfo textually, before `url.Parse`, so no parse error can echo it; `remotes.json` must be 0600 (win32 exempt); CLI telemetry (`/internal/*`) is dropped for a remote target rather than sent off-box.
- **`AO_*` env leaks into `internal/cli` tests — clear all of it, and use a shell array.** This machine exports `AO_SESSION_ID`, `AO_PROJECT_ID`, `AO_DATA_DIR`, `AO_RUN_FILE`, `AO_RUNTIME_LAUNCH_ID`, `AO_BROWSER_CAPABILITY` and more; without clearing them `internal/cli` reports ~15 failures that are pure harness leakage (see the note in `docs/upstreaming-stack-status.md`). The shell here is **zsh, which does not word-split an unquoted parameter**, so `env $UNSET go test` silently passes one giant argument and clears nothing. Always:

      args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
      env "${args[@]}" go test ./...

  Sanity check the array once per session with `env "${args[@]}" env | grep -c '^AO_'` → `0`.
- **Upstream conventions** (`AGENTS.md`): surgical changes, no drive-by cleanup, conventional commits (`feat:`/`fix:`), tabs (Go is gofmt), ≤15 non-test files per PR. The repo's pre-commit hook is **gofmt-only** — a gofmt failure there is real, and `gofmt -l <dirs>` printing nothing is the gate.
- **Scrub before every commit** (spec §3.3): the §3.3 regex over the branch's changed lines must print nothing, and a second pass over comment lines catches the bare fork-PR references the §3.3 regex does not (`#50`, `#56` — the fork's own PR numbers, embedded in ported doc comments). Both commands are in each task's commit step.
- **Test commands** (from `$CLI/backend`, with the `args` array above): `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l` over the touched directories. Upstream's `go` CI job also runs `test -race`; run it on the touched packages per branch.
- **Fork source refs** (all squash commits on `origin/develop`, verified 2026-08-24): `#41` = `ff9ea706c`, `#50` = `869832c9c`, `#52` = `f94d01a18`, `#56` = `7f9eab157`, `#58` = `c180f5566`, `#60` = `7279a8cbd`, `#61` = `e047f9c01`, `#62` = `8d556b901`, `#63` = `bbcc2d6d2`. Each is single-parent, so `git diff <sha>^1 <sha>` is that PR's whole change.
- **Deviations from the spec, decided here, each justified in its task:** (1) `resolvedBySuffix` + `TestResolvedBySuffix` + the `project add` success-line hunk move from fork #50 into **C2c**, because #60 (C2c) calls that helper and C2b/C2c must not share one; (2) fork #61 splits — its `dev.go` half is a local-only refusal and goes to **C2a**, its `pr_ref.go` half goes to **C2b**; (3) #61's new `pr_ref_test.go` lands as **`pr_ref_remote_test.go`**, because upstream now has its own `pr_ref_test.go`; (4) C3 drops #62's `preview.go` hunk (a one-sentence comment edit inside C2a's comment) so C3 does not depend on C2a; (5) C3's `lanGet` test helper gains a keep-alive-free client and raw-body reporting — see Task 6, Step 4.

---

## File structure

Worktree `$CLI` = `/Users/amongstar/dev/agent-orchestrator-up-cli` (created in Task 1, sibling of the main checkout, not under AO's managed worktree dir). `$W` = this AO session worktree, which owns the fork history the ports are read from. Paths below are relative to `$CLI`.

| Branch | Base | Fork sources | Creates | Modifies |
| --- | --- | --- | --- | --- |
| `up-c1-url` | `upstream/main` | #41 | `backend/internal/cli/remote.go`, `remote_test.go` | `cli/client.go`, `cli/root.go`, `cli/status.go`, `cli/stop.go`, `config/config.go`, `docs/cli/README.md` |
| `up-c2a-refuse-local` | `up-c1-url` | #58, #63, #61 (dev half) | `cli/remote_callbacks_test.go` | `cli/remote.go`, `remote_test.go`, `doctor.go`, `import.go`, `preview.go`, `root.go`, `start.go`, `agent_process.go`, `hooks.go`, `dev.go`, `dev_test.go` |
| `up-c2b-remote-path` | `up-c1-url` | #50 (minus the suffix), #52, #56, #61 (pr_ref half) | `cli/pr_ref_remote_test.go` | `cli/remote.go`, `remote_test.go`, `project.go`, `project_test.go`, `spawn.go`, `pr_ref.go` |
| `up-c2c-name-daemon` | `up-c1-url` | #60 + #50's `resolvedBySuffix` | — | `cli/remote.go`, `remote_test.go`, `project.go`, `project_test.go`, `session.go`, `session_test.go` |
| `up-c3-route-loopback` | `up-c1-url` | #62 (minus `preview.go`) | `cli/loopback_only_test.go` | `httpd/lan_listener.go`, `httpd/lan_listener_test.go` |
| (develop) | `plan/2026-08-24-cli` | — | `docs/upstreaming-pr-bodies/c1-url.md` … `c3-route-loopback.md` | `docs/upstreaming-stack-status.md`, this plan |

Module boundaries, fixed here so tasks agree on names (all in package `cli` unless noted):

- `remote.go` (C1) — `type remoteTarget {baseURL, token, source string}`; `remotesFile`/`remoteEntry` (`~/.ao/remotes.json`, 0600); `resolveRemoteTarget(flagURL string) (*remoteTarget, error)` → `nil, nil` for local; `normalizeRemoteURL`, `hasUserinfo`, `remotesFilePath`, `lookupRemoteEntry`; `(c *commandContext) authorize(*http.Request)`.
- `client.go` (C1) — `(c *commandContext) daemonBase() (string, error)`: the remote base URL, else the run-file loopback base gated on a live local PID exactly as before. `postLoopbackJSON` returns `nil` for a remote target.
- `status.go` (C1) — `probeHTTPError{path, status}`, `loopbackBase(port)`, `inspectRemoteDaemon`, `remoteProbeFailure`, `daemonState` gains `stateLockedOut`; `daemonStatus` gains `URL`.
- C2a adds to `remote.go` — `refuseLocalOnly(command, why string) error`, `refuseDaemonURLFlag() error`, `pinToLocalDaemon(command string) error`; and `noteIgnoredRemoteTarget(command, target string)` in `hooks.go`.
- C2b adds to `remote.go` — `checkRemoteProjectPath(path string) error`, `isAbsForSomeHost(p string) bool`, `checkRemoteImplicitProject(explicit string) error`.
- C2c adds to `remote.go` — `resolvedBySuffix() string` (empty for local); and turns `confirmProjectRemoval` / `confirmSessionCleanup` into `*commandContext` methods.
- C3 adds to `httpd/lan_listener.go` — `loopbackOnlyJSON(w, r)`, emitting `ROUTE_LOOPBACK_ONLY` at status 404; `lanControlBlock` calls it instead of `notFoundJSON`.

Every one of `refuseLocalOnly`, `refuseDaemonURLFlag`, `pinToLocalDaemon`, `checkRemoteProjectPath`, `checkRemoteImplicitProject`, `resolvedBySuffix` opens with `if c.remote == nil` and returns the local answer. That is what makes the four sibling branches independently mergeable and independently reviewable as "no `--url` ⇒ no change".

---

### Task 1: CLI stack worktree and baseline

**Files:**
- Create: worktree `$CLI` at `upstream/main` (detached — each task cuts its own branch)
- Create: `$SCRATCH/goextract.py` (a throwaway helper, never committed)

- [ ] **Step 1: Fetch upstream and create the worktree**

Run from this AO worktree:

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101
git fetch upstream --quiet && git fetch origin --quiet
export W="$PWD" CLI=/Users/amongstar/dev/agent-orchestrator-up-cli
git rev-parse upstream/main   # expect 6cba6344c7081f2984927ca5f15140f99115c5c0
git worktree add --detach "$CLI" upstream/main
git -C "$CLI" log -1 --format='%h %ad %s' --date=short
```

Expected: `6cba6344c 2026-08-24 Revert "feat(settings): restructure settings and mobile pairing (#4315)" (#4321)`. If newer, note the SHA and carry it into Task 7's hand-off.

- [ ] **Step 2: Prove the upstream tree is green before touching it**

Go only — no `npm ci`, no `node_modules`, nothing frontend.

```bash
cd "$CLI/backend"
args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" env | grep -c '^AO_'          # must print 0
env "${args[@]}" go build ./... && echo BUILD_OK
env "${args[@]}" go vet ./... && echo VET_OK
env "${args[@]}" go test ./... 2>&1 | grep -vE '^ok|no test files' ; echo "failures above? (none = green)"
```

Expected: `0`, `BUILD_OK`, `VET_OK`, and no lines between the last two. **If `internal/cli` reports ~15 failures here, the `args` array is empty** — re-read the Global Constraints note about zsh word-splitting before blaming the tree.

- [ ] **Step 3: Write the one helper the ports need**

Three of the ports take a function in its *final* `develop` form rather than replaying two fork commits over each other. This extracts one top-level Go func with its leading comment block:

```bash
export SCRATCH=/private/tmp/claude-501/-Users-amongstar--ao-data-worktrees-agent-orchestrator-agent-orchestrator-101/4be63f24-013f-5db6-995c-b56edc0de6ef/scratchpad
mkdir -p "$SCRATCH"
cat > "$SCRATCH/goextract.py" <<'PY'
#!/usr/bin/env python3
"""Print a top-level Go func (with its leading // comment block) from stdin."""
import sys
name = sys.argv[1]
lines = sys.stdin.read().split("\n")
start = next(i for i, l in enumerate(lines) if l.startswith(("func " + name + "(", "func (c *commandContext) " + name + "(")))
while start > 0 and lines[start - 1].startswith("//"):
    start -= 1
end = next(i for i in range(start, len(lines)) if lines[i] == "}")
print("\n".join(lines[start:end + 1]))
PY
git -C "$W" show origin/develop:backend/internal/cli/remote.go | python3 "$SCRATCH/goextract.py" isAbsForSomeHost | head -3
```

Expected: the first three lines of `isAbsForSomeHost`'s doc comment. Also define, in every task shell, the add-only hunk extractor used where a fork diff is a pure insertion into a file whose surrounding context does not exist on this branch:

```bash
addonly() { git -C "$W" diff "$1^1" "$1" -- "$2" | awk '/^\+\+\+/{next} /^\+/{sub(/^\+/,"");print}'; }
```

---

### Task 2: C1 — `feat(cli): target a remote daemon with --url / AO_URL`

Branch `up-c1-url` from `upstream/main`. Port of fork #41 (`ff9ea706c`), all eight files. This is the only Track C branch that adds a user-facing surface; the other four only stop it from doing the wrong thing quietly.

**Files:**
- Create: `backend/internal/cli/remote.go` (184 lines), `backend/internal/cli/remote_test.go` (447 lines, 15 test funcs)
- Modify: `backend/internal/cli/client.go`, `root.go`, `status.go`, `stop.go`, `backend/internal/config/config.go`, `docs/cli/README.md`

**Interfaces:**
- Produces: the persistent `--url` flag, `AO_URL`/`AO_TOKEN`, `~/.ao/remotes.json`, `config.StateDir()`, `commandContext.remote`, `commandContext.authorize`, `daemonState` `locked_out`.
- Consumed by: C2a, C2b, C2c (`c.remote`), C3 (`--url` in its end-to-end test).

- [ ] **Step 1: Cut the branch and bring over the test file only — watch it fail**

```bash
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101 CLI=/Users/amongstar/dev/agent-orchestrator-up-cli
cd "$CLI" && git checkout -q -b up-c1-url upstream/main
git -C "$W" diff ff9ea706c^1 ff9ea706c -- backend/internal/cli/remote_test.go | git apply --3way
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go test ./internal/cli/ 2>&1 | head -14
```

Expected (verified 2026-08-24): `Falling back to direct application...` from `git apply` (the file is new), then a build failure listing `undefined: remoteEntry`, `undefined: remotesFile`, `undefined: remotesFileName`, `undefined: normalizeRemoteURL`, `undefined: resolveRemoteTarget`, ending `FAIL … [build failed]`. That is RED: the 15 tests describe a `remote.go` that does not exist yet.

The suite it just added, by name — this is the contract C1 must satisfy:

| Test | Pins |
| --- | --- |
| `TestNormalizeRemoteURL` | scheme must be http/https; bare `host:3011` ⇒ `http://`; trailing slashes trimmed; `@` outside the authority still parses |
| `TestNormalizeRemoteURLRejectsUserinfo` | `http://user:hunter2@host` refused, and the error never echoes `hunter2` |
| `TestResolveRemoteTargetRejectsUserinfo` | the same, through the real resolution path |
| `TestResolveRemoteTargetDefaultsToLocal` | no `--url`, no `AO_URL` ⇒ `nil` — **the off-state** |
| `TestResolveRemoteTargetCredentialSources` | `AO_TOKEN` wins over `remotes.json`; missing entry and empty password are different errors |
| `TestResolveRemoteTargetRejectsLooseRemotesFilePerms` | mode looser than 0600 refused (skipped on windows) |
| `TestDaemonBaseLocalUsesRunFile` / `…RemoteSkipsRunFile` | local still gates on run-file + live PID; remote never reads it |
| `TestStatusRemoteWithoutLocalRunFile` | `ao status --url` works on a machine that has never run AO, and names the daemon |
| `TestStatusRemoteRejectsForeignService` | a non-AO JSON responder is `unhealthy`, not `ready` |
| `TestStatusRemoteReportsLockoutDistinctly` | 429 ⇒ `locked_out`, not `unhealthy` |
| `TestStatusLocalUnchangedForSameHTTPStatus` | the same 429 locally is still `unhealthy` — local output byte-identical |
| `TestStopRefusesRemoteTarget` / `…LoopbackURLToo` | the one destructive verb never follows `--url`, even one naming loopback |
| `TestPostLoopbackJSONSkippedForRemote` | CLI telemetry is dropped, not sent off-box |

- [ ] **Step 2: Port the implementation, and hand-resolve the one conflict**

```bash
cd "$CLI"
git -C "$W" diff ff9ea706c^1 ff9ea706c -- \
  backend/internal/cli/remote.go backend/internal/cli/client.go backend/internal/cli/root.go \
  backend/internal/cli/status.go backend/internal/cli/stop.go backend/internal/config/config.go \
  docs/cli/README.md | git apply --3way
git status --porcelain
```

Expected (verified 2026-08-24): `root.go`, `status.go`, `stop.go`, `config.go`, `docs/cli/README.md` apply **cleanly**; `remote.go` is added; **`client.go` applies with conflicts** — `UU backend/internal/cli/client.go`, two regions. Upstream has since split `doJSONPathWithHeaders` into a thin wrapper plus `doJSONPathWithHeadersAndTimeout`, and the fork's hunk attaches to the un-split function. Resolve both by hand — the `daemonBase()` call belongs in the *timeout* variant, and upstream's `timeout` parameter wins over the fork's `commandTimeout`:

```go
// conflict 1 — keep upstream's split, take the fork's first line of the body:
func (c *commandContext) doJSONPathWithHeaders(
	ctx context.Context,
	method, path string,
	body, out any,
	headers map[string]string,
) error {
	return c.doJSONPathWithHeadersAndTimeout(ctx, method, path, body, out, headers, commandTimeout)
}

func (c *commandContext) doJSONPathWithHeadersAndTimeout(
	ctx context.Context,
	method, path string,
	body, out any,
	headers map[string]string,
	timeout time.Duration,
) error {
	base, err := c.daemonBase()
	if err != nil {
		return err
	}
```

```go
// conflict 2 — upstream's timeout variable, the fork's #nosec justification:
	client.Timeout = timeout
	resp, err := client.Do(req) // #nosec G704 -- request target is the daemon base URL resolved above.
```

Everything else in `client.go` applied: `postLoopbackJSON` gains the `if c.remote != nil { return nil }` guard, `daemonBase()` is added above `doJSONPath`, and `c.authorize(req)` is called after the `Content-Type` header.

- [ ] **Step 3: Verify green**

```bash
cd "$CLI" && gofmt -l backend/internal/cli/ backend/internal/config/ && echo GOFMT_CLEAN
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go vet ./... && echo VET_OK
env "${args[@]}" go test ./... 2>&1 | grep -vE '^ok|no test files'
env "${args[@]}" go test -race ./internal/cli/ ./internal/config/ 2>&1 | tail -3
env "${args[@]}" go test ./internal/cli/ -v -run 'Remote|Normalize|Stop|Status|DaemonBase|PostLoopback' 2>&1 | grep -c '^--- PASS'
```

Expected: `GOFMT_CLEAN`, `VET_OK`, no failure lines, both race packages `ok`, and the PASS count ≥ 15 (subtests inflate it; the 15 top-level funcs from Step 1's table must all appear).

- [ ] **Step 4: Scrub and commit**

```bash
cd "$CLI"
git add backend/internal/cli/remote.go backend/internal/cli/remote_test.go backend/internal/cli/client.go \
  backend/internal/cli/root.go backend/internal/cli/status.go backend/internal/cli/stop.go \
  backend/internal/config/config.go docs/cli/README.md
git diff --cached -U0 | grep '^+' | grep -v '^+++' | grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 = clean)"
git diff --cached -U0 | grep '^+.*//' | grep -nE '#(4[0-9]|5[0-9]|6[0-9])\b' ; echo "fork-PR comment scan exit=$? (1 = clean)"
git commit -q -m "feat(cli): target a remote daemon with --url / AO_URL

Every ao command talks to the local daemon by reading running.json,
checking the PID is alive and calling 127.0.0.1. The new persistent
--url flag (env AO_URL) points the same commands at another machine's
LAN listener instead, skipping the run-file and the local liveness
check entirely, so it works on a machine that has never run AO.

The credential is the daemon's connection password, sent as
Authorization: Bearer — the same channel the mobile client uses
(ADR 0001). It comes from AO_TOKEN, or from ~/.ao/remotes.json, which
mirrors the mobile app's saved-node list and must be mode 0600.

The URL must not carry userinfo: it is rejected textually before
url.Parse, so no error path can echo the password. ao stop refuses a
remote target outright, including one naming loopback, so the single
destructive verb never changes behaviour based on how a URL looks. CLI
telemetry on /internal/* is dropped rather than sent off-box. A 429
from the LAN listener's lockout reports as locked_out rather than
unhealthy, a state that cannot occur against a local daemon.

With no --url and no AO_URL, resolveRemoteTarget returns nil and every
path above is the one it has always been.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Fj6tXz23Rgd29JHowjDLbk"
git push -q -u origin up-c1-url
git push -q origin up-c1-url:refs/heads/plan/2026-08-24-cli/up-c1-url
git log -1 --format=%h
```

Expected: both scrub lines print nothing and `exit=1`; both pushes succeed. Record the SHA for Task 7.

---

### Task 3: C2a — `fix(cli): refuse --url on commands that only ever act on this machine`

Branch `up-c2a-refuse-local` from `up-c1-url`. Port of fork #58 (`c180f5566`), #63 (`bbcc2d6d2`) and the `dev.go` half of #61 (`e047f9c01`).

Spec §2.5 lists the members of this PR as `doctor`, `preview`, `import`, `start`, `daemon`, `hooks`, `agent-process supervise`. `ao dev import-projects` is the eighth and belongs here rather than in C2b: it is a `refuseLocalOnly` call for the same reason as `ao import` — it resolves both data-dir paths on this machine and then hands a local path to the daemon to open on its own disk — and it is the only part of #61 that uses C2a's helper. Splitting #61 this way is what keeps C2b independent of C2a.

**Files:**
- Create: `backend/internal/cli/remote_callbacks_test.go` (241 lines)
- Modify: `backend/internal/cli/remote.go` (+75), `remote_test.go` (+126), `doctor.go`, `import.go`, `preview.go`, `root.go`, `start.go`, `agent_process.go`, `hooks.go`, `dev.go`, `dev_test.go`

**Interfaces:**
- Produces: `refuseLocalOnly`, `refuseDaemonURLFlag`, `pinToLocalDaemon`, `noteIgnoredRemoteTarget`; `newDaemonCommand` takes a `*commandContext`; `sessionPreviewPath`/`previewServerPath` become `*commandContext` methods.
- Consumes: `c.remote` (C1). Nothing else on this branch is imported by C2b/C2c/C3.

- [ ] **Step 1: Cut the branch and bring over the tests — watch them fail**

`remote.go` and `remote_test.go` cannot be applied as diffs: the fork's #58 pre-image contains #50/#52/#56, which are C2b's, so `--3way` produces one conflict spanning the whole region. Both fork hunks are **pure insertions** (`@@ -285,6 +285,46 @@` and `@@ -610,3 +610,129 @@` — no removed lines), so take the added lines directly.

```bash
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101 CLI=/Users/amongstar/dev/agent-orchestrator-up-cli
addonly() { git -C "$W" diff "$1^1" "$1" -- "$2" | awk '/^\+\+\+/{next} /^\+/{sub(/^\+/,"");print}'; }
cd "$CLI" && git checkout -q -b up-c2a-refuse-local up-c1-url
addonly c180f5566 backend/internal/cli/remote_test.go >> backend/internal/cli/remote_test.go
git -C "$W" diff bbcc2d6d2^1 bbcc2d6d2 -- backend/internal/cli/remote_callbacks_test.go | git apply --3way
git -C "$W" diff e047f9c01^1 e047f9c01 -- backend/internal/cli/dev_test.go | git apply --3way
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go test ./internal/cli/ 2>&1 | head -8
```

Expected: `undefined: refuseLocalOnly`, `undefined: refuseDaemonURLFlag`, `undefined: pinToLocalDaemon`, `FAIL … [build failed]`. Eight new test funcs are now RED:

| Test | Pins |
| --- | --- |
| `TestRefuseLocalOnlyNamesFlagAndURL` | the refusal names the flag (`--url` **or** `AO_URL`), the URL and the command; exit code 2; local ⇒ `nil` |
| `TestLocalOnlyCommandsRefuseRemoteTarget` | end to end for `doctor`, `preview`, `import`, `start` — the real assertion is an **empty request log** |
| `TestLocalOnlyCommandsUnchangedWithoutRemoteTarget` | the same commands with no target behave exactly as before |
| `TestDaemonLocalCallbacksIgnoreAOURL` | `ao hooks` / `agent-process supervise` under `AO_URL` report to the **local** daemon |
| `TestDaemonLocalCallbacksRefuseURLFlag` | an explicit `--url` on the same two is refused, exit 2 |
| `TestDaemonLocalCallbacksStayBestEffortUnderIgnoredAOURL` | the ignore is logged to `hooks.log` and never breaks the hook |
| `TestDaemonLocalCallbacksUnchangedWithoutRemoteTarget` | no target ⇒ unchanged |
| `TestDevImportProjectsRefusesRemoteTarget` | refusal happens **before** any path resolution — proven by pointing `--from-data-dir` at the target dir and asserting the same-dir error is *not* what surfaces |

- [ ] **Step 2: Port the three helpers into `remote.go`, then the call sites**

```bash
cd "$CLI"
{ addonly c180f5566 backend/internal/cli/remote.go; addonly bbcc2d6d2 backend/internal/cli/remote.go; } > /tmp/c2a-remote.txt
wc -l < /tmp/c2a-remote.txt     # expect 75
python3 - <<'PY'
p = "backend/internal/cli/remote.go"
s = open(p).read()
add = open("/tmp/c2a-remote.txt").read()
anchor = "// authorize presents the remote connection password."
assert s.count(anchor) == 1
open(p, "w").write(s.replace(anchor, add + anchor))
PY
git -C "$W" diff c180f5566^1 c180f5566 -- backend/internal/cli/doctor.go backend/internal/cli/import.go \
  backend/internal/cli/preview.go backend/internal/cli/root.go backend/internal/cli/start.go | git apply --3way
git -C "$W" diff bbcc2d6d2^1 bbcc2d6d2 -- backend/internal/cli/agent_process.go backend/internal/cli/hooks.go | git apply --3way
git -C "$W" diff e047f9c01^1 e047f9c01 -- backend/internal/cli/dev.go | git apply --3way
git status --porcelain
```

Expected (verified 2026-08-24): `doctor.go`, `import.go`, `preview.go`, `root.go`, `start.go`, `agent_process.go`, `dev.go` apply **cleanly**; **`hooks.go` applies with conflicts** (`UU`). Upstream has since added an AGY early block at the top of `runHook`, exactly where the guard goes. Resolve by putting the guard **first** — its whole point is "before anything else, including reading stdin", and an `AO_URL` that `pinToLocalDaemon` nils out still falls through to the AGY block unchanged:

```go
func (c *commandContext) runHook(ctx context.Context, agent, event string) error {
	// Before anything else, including reading stdin: a hook reports on a session
	// owned by the local daemon, so AO_URL is ignored and --url is refused.
	if err := c.pinToLocalDaemon("ao hooks"); err != nil {
		return err
	}
	if isAgyModernHookEvent(agent, event) {
		// AGY requires every modern hook handler to return a JSON object, even
		// when the command is running outside an AO-managed session.
		_, _ = fmt.Fprintln(c.deps.Out, "{}")
	}
```

- [ ] **Step 3: Scrub the ported comments of fork PR numbers**

Four comment lines carry the fork's own PR numbers and must not ship. Rewrite them to name the behaviour instead:

```bash
cd "$CLI"
python3 - <<'PY'
edits = {
    "backend/internal/cli/remote.go": [
        ("// undetectable after the fact, which is the defect class #50 and #56 ship\n// refusals for — so the message names the flag (--url or AO_URL), names the URL",
         "// undetectable after the fact, which is the defect class the other --url\n// refusals cover — so the message names the flag (--url or AO_URL), names the URL"),
        ("// Exit code 2 (usage), matching #50 and #56: passing --url to a command that",
         "// Exit code 2 (usage), like every other --url refusal: passing --url to a command that"),
    ],
    "backend/internal/cli/remote_test.go": [
        ("// and where it pointed. Exit code 2, matching #50 and #56.",
         "// and where it pointed. Exit code 2, like every other --url refusal."),
        ("// #50 shipped a Windows-only break by judging a path with filepath.IsAbs, so\n// these assert identically on every runner OS.",
         "// judging a path with filepath.IsAbs is a Windows-only break waiting to happen,\n// so these assert identically on every runner OS."),
    ],
}
for path, pairs in edits.items():
    s = open(path).read()
    for old, new in pairs:
        assert old in s, (path, old[:40])
        s = s.replace(old, new)
    open(path, "w").write(s)
print("comments rewritten")
PY
```

- [ ] **Step 4: Verify green**

```bash
cd "$CLI" && gofmt -l backend/internal/cli/ && echo GOFMT_CLEAN
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go vet ./... && echo VET_OK
env "${args[@]}" go test ./... 2>&1 | grep -vE '^ok|no test files'
env "${args[@]}" go test -race ./internal/cli/ 2>&1 | tail -2
env "${args[@]}" go test ./internal/cli/ -v -run 'RefuseLocalOnly|LocalOnlyCommands|DaemonLocalCallbacks|DevImportProjectsRefusesRemoteTarget' 2>&1 | grep -E '^--- (PASS|FAIL)'
```

Expected: `GOFMT_CLEAN`, `VET_OK`, no failure lines, race `ok`, and eight `--- PASS` lines matching Step 1's table (plus subtests).

- [ ] **Step 5: Scrub and commit**

```bash
cd "$CLI"
git add backend/internal/cli/
git diff --cached -U0 | grep '^+' | grep -v '^+++' | grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 = clean)"
git diff --cached -U0 | grep '^+.*//' | grep -nE '#(4[0-9]|5[0-9]|6[0-9])\b' ; echo "fork-PR comment scan exit=$? (1 = clean)"
git commit -q -m "fix(cli): refuse --url on commands that only ever act on this machine

ao doctor, preview, import, start, dev import-projects and the hidden
daemon command do not fail against --url: they succeed, on the wrong
machine, and say nothing about it. ao doctor --url reports the laptop's
git, tmux and data dir; ao import --url opens the laptop's database;
ao start --url opens the laptop's desktop app. A command that acted on
the wrong host cannot be caught after the fact, so each now refuses,
naming the flag that pointed off-box, the URL it points at, and where
to run the command instead. Exit code 2: this is flag misuse.

ao daemon is the one asymmetry — it refuses an explicit --url but
ignores AO_URL, because it is spawned by the desktop app rather than
typed, and refusing on an exported shell variable would turn a working
remote setup into a dead desktop app on the operator's own machine.

ao hooks and ao agent-process supervise are daemon-local callbacks: an
agent process reports activity for a session that exists here. Against
a remote daemon that reports SESSION_NOT_FOUND and exits 0, so the
local activity feed silently goes dead while the agent keeps working.
They now pin to the local daemon — an explicit --url is refused, an
exported AO_URL is ignored and the ignore is recorded in hooks.log, so
a hook is never the thing that breaks.

With no --url and no AO_URL every guard returns nil on its first line
and each of these commands behaves exactly as before.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Fj6tXz23Rgd29JHowjDLbk"
git push -q -u origin up-c2a-refuse-local
git push -q origin up-c2a-refuse-local:refs/heads/plan/2026-08-24-cli/up-c2a-refuse-local
git log -1 --format=%h
```

Expected: both scrub lines clean; both pushes succeed.

---

### Task 4: C2b — `fix(cli): judge a remote --path and a remote project by the remote host's rules`

Branch `up-c2b-remote-path` from `up-c1-url`. Port of fork #50 (`869832c9c`, minus its `resolvedBySuffix` half), #52 (`f94d01a18`), #56 (`7f9eab157`) and the `pr_ref.go` half of #61 (`e047f9c01`).

Two deliberate departures from a straight replay:

1. **`resolvedBySuffix`, `TestResolvedBySuffix` and the `project add` success-line hunk go to C2c, not here.** #60 (C2c) calls that helper; if C2b defined it, the two branches would not be siblings — whichever merged second would carry a duplicate definition. C2b keeps the *refusal* half of #50; C2c owns everything that names the daemon in output. So `project add`'s echo stays byte-identical to upstream on this branch.
2. **#52 rewrites #50's `filepath.IsAbs` check into `isAbsForSomeHost`.** Replaying #50 then #52 would land a known-broken intermediate; instead take `checkRemoteProjectPath` and `isAbsForSomeHost` in their final `develop` form.

**Files:**
- Create: `backend/internal/cli/pr_ref_remote_test.go` (159 lines — #61 added these as `pr_ref_test.go`, a name upstream now uses for its own file; a separate file also keeps the import blocks from having to be merged)
- Modify: `backend/internal/cli/remote.go` (+93), `remote_test.go` (+147), `project.go`, `project_test.go`, `spawn.go`, `pr_ref.go`

**Interfaces:**
- Produces: `checkRemoteProjectPath`, `isAbsForSomeHost`, `checkRemoteImplicitProject`; `projectSummary.Path` and the `PATH` column in `project ls`.
- Consumes: `c.remote` (C1) only.

- [ ] **Step 1: Cut the branch and bring over the tests — watch them fail**

```bash
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101 CLI=/Users/amongstar/dev/agent-orchestrator-up-cli
export SCRATCH=/private/tmp/claude-501/-Users-amongstar--ao-data-worktrees-agent-orchestrator-agent-orchestrator-101/4be63f24-013f-5db6-995c-b56edc0de6ef/scratchpad
cd "$CLI" && git checkout -q -b up-c2b-remote-path up-c1-url
{ echo
  git -C "$W" show origin/develop:backend/internal/cli/remote_test.go | python3 "$SCRATCH/goextract.py" TestCheckRemoteProjectPathRefusesHostRelative; echo
  git -C "$W" show origin/develop:backend/internal/cli/remote_test.go | python3 "$SCRATCH/goextract.py" TestCheckRemoteImplicitProjectRefusesLocalSignals; echo
  git -C "$W" show origin/develop:backend/internal/cli/remote_test.go | python3 "$SCRATCH/goextract.py" TestSpawnRemoteRefusesInheritedProjectEnv
} >> backend/internal/cli/remote_test.go
git -C "$W" diff 869832c9c^1 869832c9c -- backend/internal/cli/project_test.go | git apply --3way
git -C "$W" show e047f9c01:backend/internal/cli/pr_ref_test.go > backend/internal/cli/pr_ref_remote_test.go
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go test ./internal/cli/ 2>&1 | head -8
```

Expected: `project_test.go` applies cleanly; the build fails on `undefined: checkRemoteProjectPath` and `undefined: checkRemoteImplicitProject`. Eight new test funcs are RED:

| Test | Pins |
| --- | --- |
| `TestCheckRemoteProjectPathRefusesHostRelative` | `~`, `~/repo`, `./repo`, `../repo` refused with exit 2 and the URL named; `/srv/repo`, `C:\srv\repo`, `C:/srv/repo`, `\\server\share\r` **accepted**; `C:repo`, `\repo` refused; every form still accepted locally |
| `TestCheckRemoteImplicitProjectRefusesLocalSignals` | `AO_PROJECT_ID` / `AO_SESSION_ID` / cwd must not select a project on a remote daemon; an explicit `--project` is fine; local unchanged |
| `TestSpawnRemoteRefusesInheritedProjectEnv` | `ao spawn --url` inside a session refuses **before** any request — empty request log |
| `TestProjectList_SurfacesPath` | `project ls` table gains a `PATH` column and `--json` carries `path` |
| `TestResolvePRRefRefusesLocalGhForRemoteProject` | a numeric PR ref against a remote project does not shell out to local `gh` |
| `TestResolvePRRefAcceptsFullURLForRemoteProject` | a full PR URL is still accepted remotely |
| `TestSessionClaimPRRefusesLocalGhAgainstRemoteDaemon` | the same through `ao session claim-pr` |
| `TestResolvePRRefLocalStillUsesGhFallback` | the local `gh` fallback is untouched |

- [ ] **Step 2: Port the three helpers, in final form, then the call sites**

```bash
cd "$CLI"
{ git -C "$W" show origin/develop:backend/internal/cli/remote.go | python3 "$SCRATCH/goextract.py" checkRemoteProjectPath; echo
  git -C "$W" show origin/develop:backend/internal/cli/remote.go | python3 "$SCRATCH/goextract.py" isAbsForSomeHost; echo
  git -C "$W" show origin/develop:backend/internal/cli/remote.go | python3 "$SCRATCH/goextract.py" checkRemoteImplicitProject; echo
} > /tmp/c2b-remote.txt
python3 - <<'PY'
p = "backend/internal/cli/remote.go"
s = open(p).read()
anchor = "// authorize presents the remote connection password."
assert s.count(anchor) == 1
open(p, "w").write(s.replace(anchor, open("/tmp/c2b-remote.txt").read() + anchor))
PY
git -C "$W" diff 869832c9c^1 869832c9c -- backend/internal/cli/project.go | git apply --3way
git -C "$W" diff 7f9eab157^1 7f9eab157 -- backend/internal/cli/spawn.go | git apply --3way
git -C "$W" diff e047f9c01^1 e047f9c01 -- backend/internal/cli/pr_ref.go | git apply --3way
git status --porcelain
```

Expected (verified 2026-08-24): `project.go` and `pr_ref.go` apply **cleanly**; **`spawn.go` applies with conflicts** — upstream has added `kimchi` to the `--harness` help string since the fork's pre-image. Resolve by keeping **upstream's** `--harness` line and taking **the fork's** `--project` line (which appends `; required with --url`).

Then undo the one hunk that belongs to C2c — `project.go`'s success line, which `git apply` brought along with the rest of #50:

```bash
python3 - <<'PY'
p = "backend/internal/cli/project.go"
s = open(p).read()
old = ('\t\t\t_, err := fmt.Fprintf(cmd.OutOrStdout(), "registered project %s at %s%s\\n",\n'
       '\t\t\t\tres.Project.ID, res.Project.Path, ctx.resolvedBySuffix())\n')
new = ('\t\t\t_, err := fmt.Fprintf(cmd.OutOrStdout(), "registered project %s at %s\\n", res.Project.ID, res.Project.Path)\n')
assert old in s
open(p, "w").write(s.replace(old, new))
print("project add echo left to C2c")
PY
```

Expected: without this, the build fails with `ctx.resolvedBySuffix undefined` — which is the compiler proving the two branches really are independent.

- [ ] **Step 3: Verify green**

```bash
cd "$CLI" && gofmt -l backend/internal/cli/ && echo GOFMT_CLEAN
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go vet ./... && echo VET_OK
env "${args[@]}" go test ./... 2>&1 | grep -vE '^ok|no test files'
env "${args[@]}" go test -race ./internal/cli/ 2>&1 | tail -2
env "${args[@]}" go test ./internal/cli/ -v -run 'CheckRemoteProjectPath|CheckRemoteImplicitProject|SpawnRemoteRefusesInheritedProjectEnv|ProjectList_SurfacesPath|ResolvePRRef|SessionClaimPRRefusesLocalGh' 2>&1 | grep -E '^--- (PASS|FAIL)'
```

Expected: `GOFMT_CLEAN`, `VET_OK`, no failure lines, race `ok`, and eight `--- PASS` lines matching Step 1's table.

- [ ] **Step 4: Scrub and commit**

```bash
cd "$CLI"
git add backend/internal/cli/
git diff --cached -U0 | grep '^+' | grep -v '^+++' | grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 = clean)"
git diff --cached -U0 | grep '^+.*//' | grep -nE '#(4[0-9]|5[0-9]|6[0-9])\b' ; echo "fork-PR comment scan exit=$? (1 = clean)"
git commit -q -m "fix(cli): judge a remote --path and a remote project by the remote host's rules

With --url, the daemon resolves --path against ITS filesystem. A
host-relative path silently named a directory on the other machine:
ao project add --path '~/repo' --url <remote> registers the remote
host's ~/repo and never consults the operator's. Host-relative paths
are now refused for a remote target with exit 2. Absolute paths stay
allowed — they are meaningful on that host — and absoluteness is
judged for any host rather than for the OS running the CLI, so a
Windows operator can register /srv/repo on a Linux daemon.

The project a command acts on had the same defect from the other end:
AO_PROJECT_ID, AO_SESSION_ID and the current directory all describe
this machine, but were matched against the remote daemon's projects,
so ao spawn --url inside any AO session spawned against whatever
project on that host happened to share the inherited id. --project is
now required for a remote target: it is the one input that means the
same thing on both hosts.

Resolving a numeric PR ref falls back to running gh in the project's
checkout. With a remote target that path came from the remote daemon
and gh would run here, against this machine's checkout and this
machine's credentials, and the resulting URL was sent back as if it
had come from there. It now refuses and asks for the full PR URL.

project ls gains a PATH column, and --json a path field, both of which
the daemon has always returned and the CLI dropped on decode — the
answer to \"where does this project actually live\" is the whole
question once the answering daemon is on another machine.

Every check returns nil on its first line without a remote target, so
local behaviour is unchanged.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Fj6tXz23Rgd29JHowjDLbk"
git push -q -u origin up-c2b-remote-path
git push -q origin up-c2b-remote-path:refs/heads/plan/2026-08-24-cli/up-c2b-remote-path
git log -1 --format=%h
```

---

### Task 5: C2c — `fix(cli): name the daemon in destructive prompts and success lines`

Branch `up-c2c-name-daemon` from `up-c1-url`. Port of fork #60 (`7279a8cbd`), plus `resolvedBySuffix` and its test from #50 (`869832c9c`) and the `project add` echo hunk that calls it.

`session kill`, `session cleanup` and `project rm` are correct about which daemon they hit, but "Clean 2 terminated sessions across all projects? Type yes to confirm:" names no host, and a session or project id is not host-qualified. For a destructive verb, "you are about to do this and you cannot tell where" is the whole defect. `resolvedBySuffix()` returns `""` for a local daemon, so every local string stays byte-identical — the tests assert the local forms as exact literals for exactly that reason.

**Files:**
- Modify: `backend/internal/cli/remote.go` (+24), `remote_test.go` (+18), `project.go`, `project_test.go` (+90), `session.go`, `session_test.go` (+153)

**Interfaces:**
- Produces: `resolvedBySuffix() string`; `confirmProjectRemoval` and `confirmSessionCleanup` become `*commandContext` methods.
- Consumes: `c.remote` (C1) only.

- [ ] **Step 1: Cut the branch and bring over the tests — watch them fail**

`project_test.go` cannot be applied as a diff (#60's hunk sits after #50's `TestProjectList_SurfacesPath`, which is C2b's) — its hunk is a pure append, so take the added lines directly.

```bash
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101 CLI=/Users/amongstar/dev/agent-orchestrator-up-cli
export SCRATCH=/private/tmp/claude-501/-Users-amongstar--ao-data-worktrees-agent-orchestrator-agent-orchestrator-101/4be63f24-013f-5db6-995c-b56edc0de6ef/scratchpad
addonly() { git -C "$W" diff "$1^1" "$1" -- "$2" | awk '/^\+\+\+/{next} /^\+/{sub(/^\+/,"");print}'; }
cd "$CLI" && git checkout -q -b up-c2c-name-daemon up-c1-url
addonly 7279a8cbd backend/internal/cli/project_test.go >> backend/internal/cli/project_test.go
git -C "$W" diff 7279a8cbd^1 7279a8cbd -- backend/internal/cli/session_test.go | git apply --3way
{ echo; git -C "$W" show origin/develop:backend/internal/cli/remote_test.go | python3 "$SCRATCH/goextract.py" TestResolvedBySuffix; } >> backend/internal/cli/remote_test.go
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go test ./internal/cli/ 2>&1 | head -8
```

Expected: `session_test.go` applies cleanly; build fails on `undefined: resolvedBySuffix`. Four new test funcs are RED:

| Test | Pins |
| --- | --- |
| `TestResolvedBySuffix` | `""` for a local daemon; names the daemon for a remote one |
| `TestProjectRemoveNamesTheDaemon` | the `project rm` confirmation prompt and the "removed project" line name the remote daemon; the local forms are exact literals |
| `TestSessionKillNamesTheDaemon` | `session %s killed` and the `(workspace preserved)` variant, same rules |
| `TestSessionCleanupNamesTheDaemon` | the cleanup prompt, the dry-run line and the completion summary, same rules |

- [ ] **Step 2: Port `resolvedBySuffix` and the call sites**

```bash
cd "$CLI"
{ git -C "$W" show origin/develop:backend/internal/cli/remote.go | python3 "$SCRATCH/goextract.py" resolvedBySuffix; echo; } > /tmp/c2c-remote.txt
python3 - <<'PY'
p = "backend/internal/cli/remote.go"
s = open(p).read()
anchor = "// authorize presents the remote connection password."
assert s.count(anchor) == 1
open(p, "w").write(s.replace(anchor, open("/tmp/c2c-remote.txt").read() + anchor))
PY
git -C "$W" diff 7279a8cbd^1 7279a8cbd -- backend/internal/cli/project.go backend/internal/cli/session.go | git apply --3way
python3 - <<'PY'
# #50's project-add echo, which belongs with the helper rather than with C2b's path checks.
p = "backend/internal/cli/project.go"
s = open(p).read()
old = ('\t\t\t_, err := fmt.Fprintf(cmd.OutOrStdout(), "registered project %s at %s\\n", res.Project.ID, res.Project.Path)\n')
new = ('\t\t\t_, err := fmt.Fprintf(cmd.OutOrStdout(), "registered project %s at %s%s\\n",\n'
       '\t\t\t\tres.Project.ID, res.Project.Path, ctx.resolvedBySuffix())\n')
assert old in s
open(p, "w").write(s.replace(old, new))
PY
git status --porcelain
```

Expected: `project.go` and `session.go` apply **cleanly**.

- [ ] **Step 3: Scrub the one ported comment that names a fork PR**

```bash
cd "$CLI"
python3 - <<'PY'
p = "backend/internal/cli/session_test.go"
s = open(p).read()
old = ("// literal rather than a substring. Path-free by design (#50 shipped a\n"
       "// Windows-only break by judging a path), so these assert identically on every\n"
       "// runner OS.")
new = ("// literal rather than a substring. Path-free by design — judging a path by the\n"
       "// local OS is a Windows-only break waiting to happen — so these assert\n"
       "// identically on every runner OS.")
assert old in s
open(p, "w").write(s.replace(old, new))
print("comment rewritten")
PY
```

Also check `remote.go`: `resolvedBySuffix`'s doc comment on `develop` opens with "It began as a path echo…", which is fork-history narration. Rewrite the paragraph to describe the behaviour:

```bash
python3 - <<'PY'
p = "backend/internal/cli/remote.go"
s = open(p).read()
old = "// It began as a path echo, because the path echo alone is not a signal: the daemon resolves"
new = "// The path echo alone is not a signal, either: the daemon resolves"
assert old in s
open(p, "w").write(s.replace(old, new))
PY
gofmt -l backend/internal/cli/ && echo GOFMT_CLEAN
```

- [ ] **Step 4: Verify green**

```bash
cd "$CLI/backend"; args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go vet ./... && echo VET_OK
env "${args[@]}" go test ./... 2>&1 | grep -vE '^ok|no test files'
env "${args[@]}" go test -race ./internal/cli/ 2>&1 | tail -2
env "${args[@]}" go test ./internal/cli/ -v -run 'NamesTheDaemon|TestResolvedBySuffix' 2>&1 | grep -E '^--- (PASS|FAIL)'
```

Expected: `VET_OK`, no failure lines, race `ok`, four `--- PASS` lines.

- [ ] **Step 5: Scrub and commit**

```bash
cd "$CLI"
git add backend/internal/cli/
git diff --cached -U0 | grep '^+' | grep -v '^+++' | grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 = clean)"
git diff --cached -U0 | grep '^+.*//' | grep -nE '#(4[0-9]|5[0-9]|6[0-9])\b' ; echo "fork-PR comment scan exit=$? (1 = clean)"
git commit -q -m "fix(cli): name the daemon in destructive prompts and success lines

session kill, session cleanup and project rm hit the right daemon, but
say nothing about which one. \"Remove project \\\"api\\\"? Type the project
id to confirm:\" and \"Clean 2 terminated sessions across all projects?\"
both beg the question \"on whose machine?\", and neither a session id
nor a project id is host-qualified — the same id can exist on two
daemons. For a destructive verb, being unable to tell where you are
about to act is the defect.

The prompts and the success lines for those verbs, plus the
\"registered project ... at ...\" echo, now name the daemon when one was
given. The path echo needed it most: for an absolute path the echoed
string is byte-identical to what the operator typed, so it carried no
information about which machine resolved it.

The suffix is empty for a local daemon, so local output is unchanged —
the tests assert the local forms as exact literals rather than
substrings to keep it that way.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Fj6tXz23Rgd29JHowjDLbk"
git push -q -u origin up-c2c-name-daemon
git push -q origin up-c2c-name-daemon:refs/heads/plan/2026-08-24-cli/up-c2c-name-daemon
git log -1 --format=%h
```

---

### Task 6: C3 — `fix(daemon): distinguish a LAN policy block from a missing route`

Branch `up-c3-route-loopback` from `up-c1-url`. Port of fork #62 (`8d556b901`), minus its `preview.go` hunk.

**Why `up-c1-url` and not `upstream/main`** — the daemon-side change is genuinely independent, but the symptom it fixes is CLI-visible: an operator running `ao browser status --url <remote>` against a route the LAN listener blocks gets `ROUTE_NOT_FOUND`, reads it as "that daemon is too old", and goes off to audit daemon builds. `loopback_only_test.go` is the test that pins the fix at the layer the operator experiences it — it stands up the **real** LAN listener (`httpd.NewMobileLAN`) and drives the **real** CLI through `--url`, so the daemon's wire format and the CLI's rendering cannot drift apart. That test needs `--url` and C1's `aoHome` helper. Basing C3 on `upstream/main` would mean shipping the daemon change with listener-level tests only and no evidence it fixes the reported symptom. This matches spec §2.3's graph (`C1 --> C3`).

#62's `preview.go` hunk is one sentence inside a comment C2a introduces; dropping it is what keeps C3 independent of C2a. Say so in the PR body so a reviewer reading both PRs is not surprised.

**Files:**
- Create: `backend/internal/cli/loopback_only_test.go` (68 lines)
- Modify: `backend/internal/httpd/lan_listener.go` (+39/−3), `backend/internal/httpd/lan_listener_test.go` (+140)

**Interfaces:**
- Produces: `loopbackOnlyJSON`, and the `ROUTE_LOOPBACK_ONLY` error code at HTTP 404 for any path under a `lanControlBlockedPrefixes` entry served over the LAN listener.
- Consumes: `--url` (C1) in the CLI-side test only. Nothing in `internal/cli` production code changes.

- [ ] **Step 1: Cut the branch and apply**

```bash
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101 CLI=/Users/amongstar/dev/agent-orchestrator-up-cli
cd "$CLI" && git checkout -q -b up-c3-route-loopback up-c1-url
git -C "$W" diff 8d556b901^1 8d556b901 -- backend/internal/httpd/lan_listener.go \
  backend/internal/httpd/lan_listener_test.go backend/internal/cli/loopback_only_test.go | git apply --3way
git status --porcelain
```

Expected (verified 2026-08-24): `loopback_only_test.go` is added cleanly; **`lan_listener.go` and `lan_listener_test.go` apply with conflicts**. Both are import-block drift: the fork's pre-image imports `chi`, `httpd/webui` and `mobilebridge` in `lan_listener.go` (Track B's webui bypass, §1.4 — **not** upstream), and `net/http/cookiejar` + `regexp` in the test.

- [ ] **Step 2: Resolve the conflicts**

`lan_listener.go`, conflict 1 — upstream's import block plus **one** new line, nothing else:

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
```

`lan_listener.go`, conflict 2 — take **theirs** wholesale (the rewritten `lanControlBlock` doc comment explaining why the block stays outermost, in front of `authMiddleware`).

`lan_listener_test.go` — take neither side's import list; upstream's, plus `encoding/json` (the new `lanGet` decodes an envelope) and `chi` (the new fixture builds a real router):

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)
```

- [ ] **Step 3: Fix the two API-drift call sites**

The fork's `LANManager.Start` takes a bind host (Track B's `bind` mode, out of scope); upstream's takes a port only. Two call sites in the new tests:

```bash
cd "$CLI"
python3 - <<'PY'
for p, old, new in [
    ("backend/internal/httpd/lan_listener_test.go", '\tport, err := m.Start(0, "")\n', "\tport, err := m.Start(0)\n"),
    ("backend/internal/cli/loopback_only_test.go", 'lan.Start(0, "127.0.0.1")', "lan.Start(0)"),
]:
    s = open(p).read()
    assert old in s, p
    open(p, "w").write(s.replace(old, new))
print("Start() arity aligned with upstream")
PY
```

- [ ] **Step 4: Harden the new test helper against ephemeral-port reuse**

`lanBlockFixture` starts and stops listeners on ephemeral ports inside one package run, and `lanGet` used `http.DefaultClient` and swallowed a body that failed to decode. Under heavy load (a first `-race` build of the whole module) that surfaced once as `unauthenticated blocked path: got code "", want ROUTE_LOOPBACK_ONLY` — a failure message that says nothing about what actually came back. Give the helper its own keep-alive-free client and make it report the raw body:

```go
// lanTestClient keeps no idle connections: these fixtures start and stop
// listeners on ephemeral ports within a single package run, and a pooled
// connection must never outlive the listener it was opened to.
var lanTestClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

type envelopeBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	raw     string
}
```

and in `lanGet`, replace `http.DefaultClient.Do(req)` with `lanTestClient.Do(req)` and the decode with:

```go
	raw, _ := io.ReadAll(resp.Body)
	var body envelopeBody
	_ = json.Unmarshal(raw, &body)
	body.raw = string(raw)
	return resp.StatusCode, body
```

Then add `body.raw` / `hit.raw` to the two `t.Fatalf` calls that assert on `Code`, so any recurrence names what the listener actually returned.

- [ ] **Step 5: Verify green**

```bash
cd "$CLI" && gofmt -l backend/internal/httpd/ backend/internal/cli/ && echo GOFMT_CLEAN
cd backend && args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
env "${args[@]}" go vet ./... && echo VET_OK
env "${args[@]}" go test ./... 2>&1 | grep -vE '^ok|no test files'
env "${args[@]}" go test -race ./internal/httpd/ ./internal/cli/ 2>&1 | tail -3
env "${args[@]}" go test ./internal/httpd/ ./internal/cli/ -v \
  -run 'TestLANBlockedRouteIsNotReportedAsMissing|TestLoopbackStillServesBlockedRoute|TestUnauthenticatedLANCallerLearnsNoRouteTable|TestRemoteRendersLoopbackOnlyBlockNotMissingRoute|TestLANManagerBlocksLoopbackOnlyControlRoutes' 2>&1 | grep -E '^--- (PASS|FAIL)'
```

Expected: `GOFMT_CLEAN`, `VET_OK`, no failure lines, both race packages `ok`, and five `--- PASS` lines:

| Test | Pins |
| --- | --- |
| `TestLANManagerBlocksLoopbackOnlyControlRoutes` | upstream's existing block test still passes — the status stays 404 |
| `TestLANBlockedRouteIsNotReportedAsMissing` | a blocked route ⇒ 404 `ROUTE_LOOPBACK_ONLY`; an absent route ⇒ 404 `ROUTE_NOT_FOUND`; an ordinary route ⇒ 200 |
| `TestLoopbackStillServesBlockedRoute` | the same route over the loopback listener is untouched |
| `TestUnauthenticatedLANCallerLearnsNoRouteTable` | a registered blocked route and an absent path under the same prefix are byte-identical — the block is not a route-table oracle; outside the prefixes an unauthenticated call is still 401 |
| `TestRemoteRendersLoopbackOnlyBlockNotMissingRoute` | end to end: real LAN listener + real CLI + `--url` renders the policy block, not `ROUTE_NOT_FOUND`, and the request never reaches the router |

- [ ] **Step 6: Scrub and commit**

```bash
cd "$CLI"
git add backend/internal/httpd/ backend/internal/cli/
git diff --cached -U0 | grep '^+' | grep -v '^+++' | grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 = clean)"
git diff --cached -U0 | grep '^+.*//' | grep -nE '#(4[0-9]|5[0-9]|6[0-9])\b' ; echo "fork-PR comment scan exit=$? (1 = clean)"
git commit -q -m "fix(daemon): distinguish a LAN policy block from a missing route

The LAN listener blocks loopback-only control prefixes (/shutdown,
/internal/, /api/v1/mobile, /api/v1/dev, /api/v1/system/install) before
auth and before the router. It answered ROUTE_NOT_FOUND, which reads as
\"this daemon is too old / that endpoint does not exist\" — so an
operator who hits a deliberate policy block goes and audits daemon
builds and finds nothing wrong, because nothing is wrong.

Blocked paths now answer ROUTE_LOOPBACK_ONLY with a message that says
the route is served on the loopback listener only. The status stays
404: the route genuinely is not mounted on this listener, and holding
the status keeps existing clients' error handling working; the code and
the message carry the diagnosis.

The block stays outermost, in front of authMiddleware, and the answer
stays a constant of the AO build: every path under a blocked prefix
gets the identical response whether or not a handler exists behind it,
so this is never an oracle for which loopback-only routes a particular
daemon has. A test pins that a registered blocked route and an absent
path under the same prefix are byte-identical to an unauthenticated
caller.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Fj6tXz23Rgd29JHowjDLbk"
git push -q -u origin up-c3-route-loopback
git push -q origin up-c3-route-loopback:refs/heads/plan/2026-08-24-cli/up-c3-route-loopback
git log -1 --format=%h
```

---

### Task 7: Hand-off — extend the runbook, add the Track C PR bodies

Runs in the AO worktree on `plan/2026-08-24-cli`, extending this plan's own PR. `docs/upstreaming-stack-status.md` already documents Plan 1's Track A stack; **extend it, do not rewrite it** — Plan 1's worker owns those sections and may be editing them.

**Files:**
- Create: `docs/upstreaming-pr-bodies/c1-url.md`, `c2a-refuse-local.md`, `c2b-remote-path.md`, `c2c-name-daemon.md`, `c3-route-loopback.md`
- Modify: `docs/upstreaming-stack-status.md`

- [ ] **Step 1: Add the Track C sections to the runbook**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101
git checkout -q plan/2026-08-24-cli && git pull -q --rebase origin plan/2026-08-24-cli 2>/dev/null || true
```

Append to `docs/upstreaming-stack-status.md`, after the existing Track A material, retitling the document's opening line to say it now covers both stacks:

```markdown
---

# Track C — CLI remote target (`--url` / `AO_URL`)

Built by Plan C (`docs/superpowers/plans/2026-08-24-upstream-cli-url.md`) against the same `upstream/main @ <SHA>`. Go only: `backend/internal/cli/**` plus one function in `backend/internal/httpd/lan_listener.go`. It shares no code with Track A and can be opened in parallel with it — same reviewers, different files.

The public branch names are the clean refs below. The `plan/2026-08-24-cli/up-c*` twins on origin point at the same commits and are AO session bookkeeping only — never open an upstream PR from a namespaced ref.

## Branch topology

`up-c1-url` is the root. `up-c2a-refuse-local`, `up-c2b-remote-path`, `up-c2c-name-daemon` and `up-c3-route-loopback` are cut from it as **independent siblings** — none calls another's helpers, and each is separately reviewable as "no `--url` ⇒ no change". Open C1 first; the other four can then go in any order.

## Order of operations (human)

1. C1 goes with (or after) the RFC. It is the only Track C PR that adds a surface; the other four are bug fixes in behaviour it creates, and reviewing them before C1 exists reads as noise.
2. After C1 is squash-merged, rebase all four siblings onto `upstream/main` and open them — any order, all four at once is fine.
3. Rebase recipe (upstream squash-merges, so always `--onto` across a merged parent, never a merge):

       git fetch upstream
       git rebase --onto upstream/main up-c1-url up-c2a-refuse-local
       git push --force-with-lease origin up-c2a-refuse-local
       # (same two lines for up-c2b-remote-path, up-c2c-name-daemon, up-c3-route-loopback)

4. **Expected rebase conflicts between the siblings, and how to resolve them.** C2a, C2b and C2c each append helpers to `remote.go` immediately above `// authorize presents the remote connection password.`, and each appends tests to the end of `remote_test.go`. Once one of them merges, the next one's rebase conflicts there. Every such conflict is **additive — keep both sides**; there is no case where two siblings modify the same lines. The one exception worth reading carefully: C2b and C2c both touch `project.go`'s `project add` success line. C2c owns it (it adds `ctx.resolvedBySuffix()`); C2b leaves it at upstream's form. If C2c merges first, C2b's rebase must keep C2c's version.

## The branches

| # | Branch (on `origin`) | SHA | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- | --- |
| C1 | `up-c1-url` | `<sha>` | `upstream/main` | feat(cli): target a remote daemon with --url / AO_URL | 6 | remote ×15 |
| C2a | `up-c2a-refuse-local` | `<sha>` | `up-c1-url` | fix(cli): refuse --url on commands that only ever act on this machine | 9 | remote ×3, callbacks ×4, dev ×1 |
| C2b | `up-c2b-remote-path` | `<sha>` | `up-c1-url` | fix(cli): judge a remote --path and a remote project by the remote host's rules | 4 | remote ×3, project ×1, pr_ref ×4 |
| C2c | `up-c2c-name-daemon` | `<sha>` | `up-c1-url` | fix(cli): name the daemon in destructive prompts and success lines | 3 | remote ×1, project ×1, session ×2 |
| C3 | `up-c3-route-loopback` | `<sha>` | `up-c1-url` | fix(daemon): distinguish a LAN policy block from a missing route | 1 | lan_listener ×3, cli end-to-end ×1 |

Verified on the upstream base at build time: on every branch `go build ./...`, `go vet ./...`, `go test ./...` and `go test -race` over the touched packages are green, and `gofmt -l` prints nothing.

## Running the Go suite on a machine with an AO session

`internal/cli` reads ambient `AO_*` environment. On a box with a live AO session (`AO_SESSION_ID`, `AO_PROJECT_ID`, `AO_DATA_DIR`, `AO_RUN_FILE`, `AO_RUNTIME_LAUNCH_ID`, `AO_BROWSER_CAPABILITY`, …) that produces ~15 failures that are pure harness leakage — CI is unaffected. Clear all of it, and note that **zsh does not word-split an unquoted parameter**, so the array form is required:

    args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
    env "${args[@]}" env | grep -c '^AO_'      # must print 0
    env "${args[@]}" go test ./...

## Opening a PR (C1 first; the other four in any order after it merges)

    gh pr create --repo Untrivial-ai/agent-orchestrator --base main \
      --head AronPerez:up-c1-url \
      --title "feat(cli): target a remote daemon with --url / AO_URL" \
      --body-file docs/upstreaming-pr-bodies/c1-url.md

Bodies follow upstream's template (What / Why / How / Testing / Checklist) and are in `docs/upstreaming-pr-bodies/`. Each body's "Why" links the RFC issue number once it exists — fill `#RFC` in before opening.

## What a reviewer can verify with no `--url` and no `AO_URL`

- `resolveRemoteTarget("")` returns `nil` and every daemon call goes through the run-file path it always did (`TestResolveRemoteTargetDefaultsToLocal`, `TestDaemonBaseLocalUsesRunFile`, C1).
- Every guard added by C2a/C2b/C2c returns `nil` on its first line (`Test*UnchangedWithoutRemoteTarget`, `TestResolvedBySuffix`, `TestCheckRemoteProjectPathRefusesHostRelative`'s local half).
- Local output is asserted as exact literals, not substrings, wherever a suffix could have crept in (C2c).
- `ao status` against a local daemon still reports `unhealthy` for the same HTTP status a remote target reports `locked_out` for (`TestStatusLocalUnchangedForSameHTTPStatus`, C1).
- The loopback listener is untouched by C3 (`TestLoopbackStillServesBlockedRoute`).
```

- [ ] **Step 2: Write the five PR bodies**

Each of `docs/upstreaming-pr-bodies/c1-url.md` … `c3-route-loopback.md`, using upstream's template. Take "What" from the first paragraph of that branch's commit message and "How" from the rest — `git -C "$CLI" log -1 --format=%b <branch>`:

```markdown
## What

<the first paragraph of that branch's commit message>

## Why

Part of the CLI half of the remote-hosts series proposed in #RFC. <One sentence naming the defect: for C1, "there is no way to run ao against a daemon on another machine"; for C2a/b/c, "with --url these commands succeed on the wrong machine and say nothing about it"; for C3, "a deliberate policy block is reported with the code that means the endpoint does not exist".>

## How

<the remaining paragraphs of that branch's commit message>

## Testing

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race <the touched packages>`; `gofmt -l` clean. Counts as in the table in `docs/upstreaming-stack-status.md`. No frontend file and no OpenAPI surface is touched, so the `frontend`, `renderer-smoke` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
```

C3's body additionally carries one line a reviewer will otherwise ask about: *"`ao preview` also renders this block; the one-sentence comment update in `cli/preview.go` rides with the `--url` refusal PR instead, so this PR stays daemon-side."*

- [ ] **Step 3: Scrub, commit, push**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-101
grep -rnE "amongstar|/Users/" docs/upstreaming-pr-bodies/c*.md ; echo "bodies scrub exit=$? (1 = clean — the status doc may name local paths, the bodies may not)"
git add docs/upstreaming-stack-status.md docs/upstreaming-pr-bodies/ docs/superpowers/plans/2026-08-24-upstream-cli-url.md
git commit -q -m "docs: hand-off for the upstream CLI remote-target stack

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Fj6tXz23Rgd29JHowjDLbk"
git push -q origin plan/2026-08-24-cli
```

Expected: the bodies scrub prints nothing; push succeeds; the plan's PR updates in place. Report the PR link, the five branch names with SHAs, and the upstream SHA the stack is based on.

---

## Self-review

**Spec coverage.** §2.5 C1 (fork #41 minus docs-web) → Task 2, all eight files including `docs/cli/README.md`, tests as the spec lists them (scheme, userinfo, 0600, `AO_TOKEN` precedence, `ao status --url` names the daemon), and the spec's "SSH entries are ignored, not rejected" point is already true of the ported `lookupRemoteEntry` (`continue` past an unparseable URL) and is stated in C1's PR body. §2.5 C2 as **three** PRs, with the spec's own grouping — (i) refuse local-only → Task 3, covering all seven commands the spec names plus `ao dev import-projects`; (ii) `--path` by the remote host's rules and no local `gh`/path resolution → Task 4; (iii) name the daemon in destructive prompts and success lines → Task 5. §2.5 C3 (~20-line `ROUTE_LOOPBACK_ONLY` hunk + test) → Task 6; §2.5's optional #49 follow-on is out of scope and not mentioned in any body. §2.1 rules: every branch off `upstream/main` (C1) or off C1, no six-deep chain, `rebase --onto` recipe in the runbook, ≤15 non-test files everywhere (max is C2a at 9), tests ride with the code they pin, fork-isms stripped. §1.4's `lan_listener.go` hazard → Task 6 Step 2 takes the `ROUTE_LOOPBACK_ONLY` hunk alone and explicitly drops Track B's `webui`/`chi`/`mobilebridge` imports. §3.3 scrub → a two-command gate before every commit, plus three explicit comment rewrites (Tasks 3 and 5) for the bare fork-PR numbers the §3.3 regex does not catch.

**Placeholder scan.** Every port step carries a command that was run against `upstream/main @ 6cba6344c` on 2026-08-24 and its actual outcome, including which files conflict and how to resolve each: `client.go` (C1, upstream's `doJSONPathWithHeadersAndTimeout` split), `hooks.go` (C2a, upstream's AGY block), `spawn.go` (C2b, upstream's `kimchi` harness), `project_test.go` and `remote.go`/`remote_test.go` (C2a/C2c, add-only extraction), `lan_listener.go` + `lan_listener_test.go` (C3, import drift and `Start` arity). The two templated spots — the runbook's SHA column and the PR bodies' What/How — each name the exact command that produces the text.

**Type consistency.** `commandContext.remote` is `*remoteTarget` in C1 and is read, never re-typed, by all four siblings. `refuseLocalOnly(command, why string) error` has the same signature in C2a's `remote.go`, its two callers in `hooks.go`/`agent_process.go` via `pinToLocalDaemon`, and its six direct callers. `resolvedBySuffix() string` is defined once, in C2c, and called from `project.go` (×2) and `session.go` (×4) on that branch only — C2b's Step 2 explicitly reverts the one call site `git apply` drags in, and the compiler failure that follows if it is skipped is documented as the proof of independence. `envelopeBody` gains an unexported `raw` field in C3 Step 4; it is a test-local type in package `httpd` with no other consumer.

**Independence claim, checked by construction.** C2a defines `refuseLocalOnly`/`refuseDaemonURLFlag`/`pinToLocalDaemon`; C2b defines `checkRemoteProjectPath`/`isAbsForSomeHost`/`checkRemoteImplicitProject`; C2c defines `resolvedBySuffix`; C3 defines `loopbackOnlyJSON`. No name in any of those four lists appears in another branch's diff. The only shared file regions are append points, which the runbook's rebase note calls out as keep-both.
