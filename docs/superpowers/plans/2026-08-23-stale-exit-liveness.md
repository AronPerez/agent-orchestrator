# Stale-Exit Liveness Guard (#114, effects 3+4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A nested agent's `SessionEnd` hook can no longer mark a live parent session `exited` (A2), and a session already stuck in a stale `exited` state self-heals the moment the user sends to it (A1).

**Architecture:** Lifecycle gains a late-bound `ExactSupervisedProcessInspector` edge (same pattern as its existing `completionTerminator`). `ApplyActivitySignal` probes the supervisor before accepting a hook-driven `exited`; the supervisor's own `process-exited` report and the reaper's probe-driven writes are exempt because they *are* the authority. A new `ClearStaleExit` on lifecycle revives a provably-alive session to `idle`; session_manager calls it once, late-bound, at its four `ErrAgentExited` refusal points and retries delivery once.

**Tech Stack:** Go (backend only). No API, schema, or frontend changes.

**Spec:** GitHub issue #114 + its scope comment (`gh issue view 114 --repo AronPerez/agent-orchestrator --comments`); state-machine doc: https://claude.ai/code/artifact/195d23ef-98b5-4a3d-a6f6-6ad6bf078ef5. Line refs below verified against `origin/develop @ 74834f0a5`.

## Global Constraints

- **Fail open, always.** No inspector wired, probe error, missing `RuntimeHandleID`/`RuntimeLaunchID`, store read error → apply the exit exactly as today. A probe error is never a liveness verdict (`ports/outbound.go:145-148` says exactly this for the sibling interface). The inverse bug — a session that can never be marked exited — is worse than #114.
- **Never probe while holding `m.mu`.** The probe shells out to `ps` on some runtimes. Probe before locking; the locked write re-checks state.
- **Never gate the authorities.** `Event == "process-exited"` (`cli/agent_process.go:90` — the supervisor's own `wait4` report) must bypass the probe untouched. The reaper's probe-driven writes (`lifecycle/manager.go:375-418`, via `RuntimeFacts`) are not in `ApplyActivitySignal` and must not be modified.
- **Do not close #114.** Effects 1+2 (fact overwrite, resume-handle replacement) stay open on it by explicit decision; the PR says "addresses effects 3+4 of #114", not "fixes".
- Backend tests: clear all ambient `AO_*` env first (`env | grep ^AO_`; unset them) or `internal/cli` produces phantom failures.
- Pre-commit hook is gofmt-only and real. Branch: `ao/agent-orchestrator-<your-session-number>/114-stale-exit-liveness`, base `develop` (≥ `74834f0a5`). Never bare `git stash`.
- Every new test must be falsified: revert the guard it tests, watch it go red, restore. Say so in the PR.

---

### Task 1: Lifecycle probe seam — `SetExitInspector` + `agentProvablyAlive`

**Files:**
- Modify: `backend/internal/lifecycle/manager.go` (struct ~`:158-198`, setters ~`:224-236`)
- Test: `backend/internal/lifecycle/stale_exit_test.go` (new)

**Interfaces:**
- Consumes: `ports.ExactSupervisedProcessInspector` (`ports/outbound.go:158-161`) — `IsExactSupervisedProcessAlive(ctx, ports.RuntimeHandle, ports.SupervisedProcessRef) (bool, error)`; `domain.SessionRecord.Metadata.{RuntimeHandleID,RuntimeLaunchID}` (`domain/session.go:33-34`).
- Produces: `(*Manager).SetExitInspector(insp ports.ExactSupervisedProcessInspector)`; unexported `(m *Manager) agentProvablyAlive(ctx context.Context, rec domain.SessionRecord) bool`; const `eventProcessExited = "process-exited"`. Tasks 2–3 call `agentProvablyAlive`; Task 5 calls `SetExitInspector`.

- [ ] **Step 1: Write the failing test**

In new `backend/internal/lifecycle/stale_exit_test.go` (package `lifecycle`; reuse `working(...)` and `newFakeAgentSwitchLifecycleStore()` from the neighboring test files — they are proven against `New`):

```go
package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeExitInspector records probe calls; used by every task in this plan.
type fakeExitInspector struct {
	mu    sync.Mutex
	alive bool
	err   error
	calls int
}

func (f *fakeExitInspector) IsExactSupervisedProcessAlive(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.alive, f.err
}

func (f *fakeExitInspector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func supervisedRec(id string) domain.SessionRecord {
	rec := working(id)
	rec.Metadata.RuntimeHandleID = "handle-1"
	rec.Metadata.RuntimeLaunchID = "launch-1"
	return rec
}

// agentProvablyAlive answers true ONLY on a wired inspector returning
// (true, nil) for a record carrying both handle and launch ids. Every other
// combination fails open (false → the exit is applied as today).
func TestAgentProvablyAliveFailsOpen(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inspector *fakeExitInspector // nil = not wired
		rec       domain.SessionRecord
		want      bool
	}{
		{"alive and fully identified", &fakeExitInspector{alive: true}, supervisedRec("s1"), true},
		{"probe says dead", &fakeExitInspector{alive: false}, supervisedRec("s1"), false},
		{"probe errors", &fakeExitInspector{alive: true, err: errors.New("ps failed")}, supervisedRec("s1"), false},
		{"no inspector wired", nil, supervisedRec("s1"), false},
		{"no runtime handle", &fakeExitInspector{alive: true}, func() domain.SessionRecord {
			r := supervisedRec("s1")
			r.Metadata.RuntimeHandleID = ""
			return r
		}(), false},
		{"no launch id", &fakeExitInspector{alive: true}, func() domain.SessionRecord {
			r := supervisedRec("s1")
			r.Metadata.RuntimeLaunchID = ""
			return r
		}(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(newFakeAgentSwitchLifecycleStore(), &fakeMessenger{})
			if tc.inspector != nil {
				m.SetExitInspector(tc.inspector)
			}
			if got := m.agentProvablyAlive(context.Background(), tc.rec); got != tc.want {
				t.Fatalf("agentProvablyAlive = %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/lifecycle/ -run TestAgentProvablyAliveFailsOpen -v`
Expected: FAIL — `m.SetExitInspector undefined` / `m.agentProvablyAlive undefined` (compile error is the failure).

- [ ] **Step 3: Write minimal implementation**

In `lifecycle/manager.go`. Struct field next to `completionTerminator` (~`:167`), setter next to `SetCompletionTerminator` (~`:224`), helper nearby:

```go
// eventProcessExited is the supervisor's own exit report (cli/agent_process.go:90).
// It comes from the process that wait4()ed the agent and is never probed.
const eventProcessExited = "process-exited"
```

```go
	// exitInspector is late-bound for the same dependency-cycle reason as
	// completionTerminator: the inspector is the session runtime, which is
	// constructed after this reducer. Nil means hook-reported exits apply
	// unprobed, exactly as before #114.
	exitInspector ports.ExactSupervisedProcessInspector
```

```go
// SetExitInspector wires the supervised-process liveness probe used to refuse
// a hook-reported exit for an agent that is provably still alive (#114,
// effects 3+4). Wired once during daemon assembly.
func (m *Manager) SetExitInspector(insp ports.ExactSupervisedProcessInspector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exitInspector = insp
}

// agentProvablyAlive reports whether the session's exact supervised agent
// process is confirmed alive right now. Anything short of a clean, fully
// identified "alive" answers false: an inspection error is never a liveness
// verdict, and a false here merely applies the exit as it always did.
// Callers must NOT hold m.mu — the probe may shell out to ps.
func (m *Manager) agentProvablyAlive(ctx context.Context, rec domain.SessionRecord) bool {
	m.mu.Lock()
	insp := m.exitInspector
	m.mu.Unlock()
	if insp == nil || rec.Metadata.RuntimeHandleID == "" || rec.Metadata.RuntimeLaunchID == "" {
		return false
	}
	alive, err := insp.IsExactSupervisedProcessAlive(ctx,
		ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID},
		ports.SupervisedProcessRef{SessionID: rec.ID, LaunchID: rec.Metadata.RuntimeLaunchID})
	return err == nil && alive
}
```

(If `working(...)`'s record shape differs from `SessionRecord.ID` naming, follow the field names the neighboring tests actually use — do not invent new ones.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/lifecycle/ -run TestAgentProvablyAliveFailsOpen -v`
Expected: PASS (all 6 subtests).

- [ ] **Step 5: Commit**

```bash
cd backend && gofmt -l ./internal/lifecycle/ && cd ..
git add backend/internal/lifecycle/manager.go backend/internal/lifecycle/stale_exit_test.go
git commit -m "feat(lifecycle): late-bound supervised-liveness probe seam"
```

---

### Task 2: A2 — gate the hook-driven `exited` write in `ApplyActivitySignal`

**Files:**
- Modify: `backend/internal/lifecycle/manager.go:439-460` (top of `ApplyActivitySignal`, BEFORE the staging call at `:477`)
- Test: `backend/internal/lifecycle/stale_exit_test.go`

**Interfaces:**
- Consumes: Task 1's `agentProvablyAlive`, `eventProcessExited`, `fakeExitInspector`, `supervisedRec`.
- Produces: behavior only — no new symbols.

- [ ] **Step 1: Write the failing tests**

Append to `stale_exit_test.go`. `ctx` is the package-level context the existing lifecycle tests use (see `nested_agent_guard_test.go`); the signal shape mirrors those tests:

```go
func exitSignal(event string) ports.ActivitySignal {
	return ports.ActivitySignal{
		Valid: true, State: domain.ActivityExited,
		Event: event, LaunchID: "launch-1",
	}
}

func sessionState(t *testing.T, store *fakeAgentSwitchLifecycleStore, id domain.SessionID) domain.ActivityState {
	t.Helper()
	rec, ok, err := store.GetSession(ctx, id)
	if err != nil || !ok {
		t.Fatalf("GetSession(%s): ok=%v err=%v", id, ok, err)
	}
	return rec.Activity.State
}

// A hook-reported exit for a provably-alive agent is dropped: this is the
// nested-agent poisoning measured in #114 (session marked exited while its
// real agent sat idle; ao send then refused with AGENT_EXITED).
func TestHookExitDroppedWhenAgentProvablyAlive(t *testing.T) {
	store := newFakeAgentSwitchLifecycleStore()
	rec := supervisedRec("s1")
	store.setSession(rec)
	insp := &fakeExitInspector{alive: true}
	m := New(store, &fakeMessenger{})
	m.SetExitInspector(insp)

	if err := m.ApplyActivitySignal(ctx, rec.ID, exitSignal("session-end")); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	if got := sessionState(t, store, rec.ID); got == domain.ActivityExited {
		t.Fatalf("a provably-alive agent was marked exited")
	}
	if insp.callCount() == 0 {
		t.Fatal("the liveness probe was never consulted")
	}
}

// Everything short of a clean "alive" applies the exit exactly as before:
// dead probe, probe error, and no inspector at all.
func TestHookExitAppliedUnlessProvablyAlive(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inspector *fakeExitInspector
	}{
		{"probe says dead", &fakeExitInspector{alive: false}},
		{"probe errors — fail open", &fakeExitInspector{alive: true, err: errors.New("ps failed")}},
		{"no inspector wired — fail open", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAgentSwitchLifecycleStore()
			rec := supervisedRec("s1")
			store.setSession(rec)
			m := New(store, &fakeMessenger{})
			if tc.inspector != nil {
				m.SetExitInspector(tc.inspector)
			}
			if err := m.ApplyActivitySignal(ctx, rec.ID, exitSignal("session-end")); err != nil {
				t.Fatalf("ApplyActivitySignal: %v", err)
			}
			if got := sessionState(t, store, rec.ID); got != domain.ActivityExited {
				t.Fatalf("exit not applied: state=%s", got)
			}
		})
	}
}

// The supervisor's own process-exited report ran wait4 on the agent — it IS
// the authority and must never be probed.
func TestSupervisorExitNeverProbed(t *testing.T) {
	store := newFakeAgentSwitchLifecycleStore()
	rec := supervisedRec("s1")
	store.setSession(rec)
	insp := &fakeExitInspector{alive: true} // even a live-looking probe must not matter
	m := New(store, &fakeMessenger{})
	m.SetExitInspector(insp)

	if err := m.ApplyActivitySignal(ctx, rec.ID, exitSignal("process-exited")); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}
	if got := sessionState(t, store, rec.ID); got != domain.ActivityExited {
		t.Fatalf("supervisor-reported exit not applied: state=%s", got)
	}
	if insp.callCount() != 0 {
		t.Fatalf("supervisor exit was probed %d times; the wait4 report is authoritative", insp.callCount())
	}
}
```

- [ ] **Step 2: Run tests to verify the right ones fail**

Run: `cd backend && go test ./internal/lifecycle/ -run 'TestHookExit|TestSupervisorExit' -v`
Expected: `TestHookExitDroppedWhenAgentProvablyAlive` FAILS ("a provably-alive agent was marked exited"). The other two PASS already — they pin current behavior so the gate cannot overreach.

- [ ] **Step 3: Implement the gate**

At the top of `ApplyActivitySignal` (`manager.go:439`), before any locking and before the staging call at `:477`:

```go
	// #114 effects 3+4: a nested agent inheriting AO_SESSION_ID can report a
	// SessionEnd for a parent whose real agent is alive and idle. A hook is
	// only a claim; the supervisor is the authority. Probe before accepting a
	// hook-driven exit — and only a hook-driven one: process-exited comes from
	// the wait4 that reaped the agent, and the reaper path never enters here.
	// Probing before m.mu is deliberate (the probe may shell out); the window
	// between probe and write is closed by the supervisor/reaper backstops.
	if s.Valid && s.State == domain.ActivityExited && s.Event != eventProcessExited {
		if rec, ok, err := m.store.GetSession(ctx, id); err == nil && ok && m.agentProvablyAlive(ctx, rec) {
			// Mirror the rejection-logging idiom the cwd guard uses in this
			// file (#116 F3) — same logger, reason "stale_exit_dropped".
			return nil
		}
	}
```

Use the exact logging call shape the cwd-guard rejection uses in this file (added by #116; search `signalCWDBelongsToSession` call site at `:534` and copy its rejection log line, changing only the reason string).

- [ ] **Step 4: Run tests to verify all pass**

Run: `cd backend && go test ./internal/lifecycle/ -v`
Expected: PASS — the three new tests AND every pre-existing lifecycle test (the staging/cwd-guard suites prove the gate didn't disturb the signal path).

- [ ] **Step 5: Falsify, then commit**

Temporarily change the gate's condition to `false &&`, run `-run TestHookExitDroppedWhenAgentProvablyAlive`, confirm RED; restore, confirm GREEN.

```bash
git add backend/internal/lifecycle/manager.go backend/internal/lifecycle/stale_exit_test.go
git commit -m "fix(lifecycle): drop a hook-reported exit for a provably-alive agent (#114 effect 3)"
```

---

### Task 3: `ClearStaleExit` — lifecycle-owned revival for already-poisoned sessions

**Files:**
- Modify: `backend/internal/lifecycle/manager.go`
- Test: `backend/internal/lifecycle/stale_exit_test.go`

**Interfaces:**
- Consumes: Task 1's `agentProvablyAlive`; the existing `m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool)) error` (used at `:375`).
- Produces: `(*Manager).ClearStaleExit(ctx context.Context, id domain.SessionID) (bool, error)` — Task 4's session_manager edge calls exactly this signature.

- [ ] **Step 1: Write the failing test**

```go
// A2 stops new poisoning; ClearStaleExit heals sessions poisoned before it
// shipped (or in the probe race window) at the moment the user acts on them.
func TestClearStaleExit(t *testing.T) {
	exited := func() domain.SessionRecord {
		r := supervisedRec("s1")
		r.Activity = domain.Activity{State: domain.ActivityExited}
		return r
	}
	for _, tc := range []struct {
		name        string
		rec         domain.SessionRecord
		inspector   *fakeExitInspector
		wantCleared bool
		wantState   domain.ActivityState
	}{
		{"stale exit, agent alive → revived to idle", exited(), &fakeExitInspector{alive: true}, true, domain.ActivityIdle},
		{"real exit, agent gone → untouched", exited(), &fakeExitInspector{alive: false}, false, domain.ActivityExited},
		{"probe error → untouched (fail open)", exited(), &fakeExitInspector{alive: true, err: errors.New("ps failed")}, false, domain.ActivityExited},
		{"no inspector → untouched", exited(), nil, false, domain.ActivityExited},
		{"not exited → no-op", supervisedRec("s1"), &fakeExitInspector{alive: true}, false, working("s1").Activity.State},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAgentSwitchLifecycleStore()
			store.setSession(tc.rec)
			m := New(store, &fakeMessenger{})
			if tc.inspector != nil {
				m.SetExitInspector(tc.inspector)
			}
			cleared, err := m.ClearStaleExit(ctx, tc.rec.ID)
			if err != nil {
				t.Fatalf("ClearStaleExit: %v", err)
			}
			if cleared != tc.wantCleared {
				t.Fatalf("cleared = %v, want %v", cleared, tc.wantCleared)
			}
			if got := sessionState(t, store, tc.rec.ID); got != tc.wantState {
				t.Fatalf("state = %s, want %s", got, tc.wantState)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/lifecycle/ -run TestClearStaleExit -v`
Expected: FAIL — `m.ClearStaleExit undefined`.

- [ ] **Step 3: Implement**

```go
// ClearStaleExit revives a session whose exited state is provably false — the
// supervised agent is alive right now. It exists for sessions poisoned before
// the ApplyActivitySignal gate shipped, and for the gate's probe race window.
// Returns true only when it actually flipped exited → idle. The probe runs
// before m.mu (it may shell out); mutate re-checks under the lock, so a real
// exit landing in between wins.
func (m *Manager) ClearStaleExit(ctx context.Context, id domain.SessionID) (bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	if rec.IsTerminated || rec.Activity.State != domain.ActivityExited {
		return false, nil
	}
	if !m.agentProvablyAlive(ctx, rec) {
		return false, nil
	}
	cleared := false
	err = m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || cur.Activity.State != domain.ActivityExited {
			return cur, false
		}
		next := cur
		// Idle, not active: the measured #114 canary left the real agent
		// sitting idle. Idle is also what message-delivery readiness polls for.
		next.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
		cleared = true
		return next, true
	})
	return cleared, err
}
```

If `m.mutate`'s callback signature or update semantics differ from the `:375` usage in any detail, follow `:375` — it is the canonical caller.

- [ ] **Step 4: Run tests, then falsify**

Run: `cd backend && go test ./internal/lifecycle/ -v` — all PASS.
Falsify: make `ClearStaleExit` return `(false, nil)` unconditionally → `TestClearStaleExit` first subtest RED; restore → GREEN.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/lifecycle/manager.go backend/internal/lifecycle/stale_exit_test.go
git commit -m "feat(lifecycle): ClearStaleExit revives a provably-alive session (#114 effect 4 groundwork)"
```

---

### Task 4: A1 — retry-once at session_manager's four `ErrAgentExited` refusal points

**Files:**
- Modify: `backend/internal/session_manager/manager.go` (struct + two delivery switches at `:2955-2970`, `:4140-4155`)
- Create: `backend/internal/session_manager/stale_exit_retry.go`
- Modify: `backend/internal/session_manager/message_delivery.go:34-36, :68-70`
- Test: `backend/internal/session_manager/stale_exit_retry_test.go` (new)

**Interfaces:**
- Consumes: `sessionguard.Outcome` / `sessionguard.SuppressedExited`; Task 3's `ClearStaleExit(ctx, id) (bool, error)`.
- Produces: `type staleExitClearer interface { ClearStaleExit(context.Context, domain.SessionID) (bool, error) }`; `(*Manager).SetStaleExitClearer(c staleExitClearer)`; `(*Manager).RuntimeExactInspector() (ports.ExactSupervisedProcessInspector, bool)`; package-level `deliverWithStaleExitRetry(ctx, id, clearer, deliver)` — Task 5 wires the setter.

- [ ] **Step 1: Write the failing test**

New `backend/internal/session_manager/stale_exit_retry_test.go`:

```go
package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// (If this package is named other than `sessionmanager`, match the package
// clause of manager.go — do not rename anything.)

type fakeClearer struct {
	cleared bool
	err     error
	calls   int
}

func (f *fakeClearer) ClearStaleExit(context.Context, domain.SessionID) (bool, error) {
	f.calls++
	return f.cleared, f.err
}

// One retry, and only when lifecycle actually cleared a stale exit. This is
// #114 effect 4: the user's send is the moment a poisoned session can heal.
func TestDeliverWithStaleExitRetry(t *testing.T) {
	id := domain.SessionID("s1")
	for _, tc := range []struct {
		name         string
		outcomes     []sessionguard.Outcome
		clearer      *fakeClearer // nil = not wired
		wantOutcome  sessionguard.Outcome
		wantDelivers int
	}{
		{"exited + cleared → retried once, delivered",
			[]sessionguard.Outcome{sessionguard.SuppressedExited, sessionguard.DeliveredDirect},
			&fakeClearer{cleared: true}, sessionguard.DeliveredDirect, 2},
		{"exited + not cleared (real exit) → refusal stands",
			[]sessionguard.Outcome{sessionguard.SuppressedExited},
			&fakeClearer{cleared: false}, sessionguard.SuppressedExited, 1},
		{"exited + clearer errors → refusal stands",
			[]sessionguard.Outcome{sessionguard.SuppressedExited},
			&fakeClearer{cleared: true, err: errors.New("store down")}, sessionguard.SuppressedExited, 1},
		{"exited + no clearer wired → refusal stands",
			[]sessionguard.Outcome{sessionguard.SuppressedExited},
			nil, sessionguard.SuppressedExited, 1},
		{"still exited after clear → second refusal stands, no third try",
			[]sessionguard.Outcome{sessionguard.SuppressedExited, sessionguard.SuppressedExited},
			&fakeClearer{cleared: true}, sessionguard.SuppressedExited, 2},
		{"non-exited outcome → untouched, clearer never called",
			[]sessionguard.Outcome{sessionguard.SuppressedTerminated},
			&fakeClearer{cleared: true}, sessionguard.SuppressedTerminated, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivers := 0
			deliver := func() (sessionguard.Outcome, error) {
				out := tc.outcomes[delivers]
				delivers++
				return out, nil
			}
			var clearer staleExitClearer
			if tc.clearer != nil {
				clearer = tc.clearer
			}
			out, err := deliverWithStaleExitRetry(context.Background(), id, clearer, deliver)
			if err != nil {
				t.Fatalf("deliverWithStaleExitRetry: %v", err)
			}
			if out != tc.wantOutcome {
				t.Fatalf("outcome = %v, want %v", out, tc.wantOutcome)
			}
			if delivers != tc.wantDelivers {
				t.Fatalf("deliver called %d times, want %d", delivers, tc.wantDelivers)
			}
			if tc.name == "non-exited outcome → untouched, clearer never called" && tc.clearer.calls != 0 {
				t.Fatalf("clearer consulted on a non-exited outcome")
			}
		})
	}
}
```

(`sessionguard.DeliveredDirect`: use whatever non-suppressed success value `sessionguard/guard.go`'s `Outcome` actually defines — read the const block near `guard.go:55` and substitute; the test's logic does not depend on which.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run TestDeliverWithStaleExitRetry -v`
Expected: FAIL — `deliverWithStaleExitRetry` / `staleExitClearer` undefined.

- [ ] **Step 3: Implement**

New `backend/internal/session_manager/stale_exit_retry.go`:

```go
package sessionmanager

import (
	"context"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// staleExitClearer is lifecycle.Manager's ClearStaleExit, late-bound here for
// the same cycle reason lifecycle late-binds its completionTerminator to this
// package (daemon.go wires both, side by side).
type staleExitClearer interface {
	ClearStaleExit(ctx context.Context, id domain.SessionID) (bool, error)
}

// deliverWithStaleExitRetry runs deliver once; if the guard refused because the
// session reads exited (#114: possibly a nested agent's lie), asks lifecycle to
// verify-and-clear, and on a genuine clear retries exactly once. Every failure
// mode leaves today's behavior untouched.
func deliverWithStaleExitRetry(ctx context.Context, id domain.SessionID, clearer staleExitClearer,
	deliver func() (sessionguard.Outcome, error)) (sessionguard.Outcome, error) {
	outcome, err := deliver()
	if err != nil || outcome != sessionguard.SuppressedExited || clearer == nil {
		return outcome, err
	}
	cleared, clearErr := clearer.ClearStaleExit(ctx, id)
	if clearErr != nil || !cleared {
		return outcome, err
	}
	return deliver()
}
```

In `manager.go`: add to the struct a `staleExitMu sync.RWMutex` + `staleExit staleExitClearer` pair and:

```go
// SetStaleExitClearer wires lifecycle's stale-exit revival into delivery
// refusal. Wired once during daemon assembly, next to SetCompletionTerminator.
func (m *Manager) SetStaleExitClearer(c staleExitClearer) {
	m.staleExitMu.Lock()
	defer m.staleExitMu.Unlock()
	m.staleExit = c
}

func (m *Manager) staleExitClearerRef() staleExitClearer {
	m.staleExitMu.RLock()
	defer m.staleExitMu.RUnlock()
	return m.staleExit
}

// RuntimeExactInspector exposes the runtime's strict liveness probe so daemon
// assembly can hand it to lifecycle without knowing the runtime's concrete type.
func (m *Manager) RuntimeExactInspector() (ports.ExactSupervisedProcessInspector, bool) {
	insp, ok := m.runtime.(ports.ExactSupervisedProcessInspector)
	return insp, ok
}
```

Wrap the two delivery calls. At `manager.go:2955`:

```go
	outcome, err := deliverWithStaleExitRetry(ctx, id, m.staleExitClearerRef(), func() (sessionguard.Outcome, error) {
		return m.messenger.DeliverWithPostWrite(ctx, id, message, afterWrite)
	})
```

At `manager.go:~4140` (both branches of the `if/else` feed the same wrapper):

```go
	outcome, err = deliverWithStaleExitRetry(ctx, id, m.staleExitClearerRef(), func() (sessionguard.Outcome, error) {
		if <existing condition> {
			return m.messenger.DeliverWithPostWrite(ctx, id, prompt, <existing afterWrite>)
		}
		return m.messenger.Deliver(ctx, id, prompt)
	})
```

(Keep the existing `if/else` bodies verbatim inside the closure — only the call wrapping changes; the `switch outcome` blocks below stay untouched.)

In `message_delivery.go`, both `ErrAgentExited` returns (`:34`, `:69`) become:

```go
		if rec.Activity.State == domain.ActivityExited {
			if clearer := m.staleExitClearerRef(); clearer != nil {
				if cleared, clearErr := clearer.ClearStaleExit(ctx, id); clearErr == nil && cleared {
					continue // re-read; a healed session proceeds to readiness
				}
			}
			return ErrAgentExited
		}
```

For the pre-loop check at `:34` (no loop to `continue`): on a genuine clear, fall through instead of returning — the readiness loop below re-reads. Shape:

```go
	if rec.Activity.State == domain.ActivityExited {
		cleared := false
		if clearer := m.staleExitClearerRef(); clearer != nil {
			if ok, clearErr := clearer.ClearStaleExit(ctx, id); clearErr == nil && ok {
				cleared = true
			}
		}
		if !cleared {
			return ErrAgentExited
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/session_manager/ -run TestDeliverWithStaleExitRetry -v` — PASS.
Run: `cd backend && go build ./... && go test ./internal/session_manager/` — full package green (pre-existing tests prove the wrapped call sites still behave).

- [ ] **Step 5: Falsify, then commit**

Falsify: make `deliverWithStaleExitRetry` skip the retry (`return outcome, err` unconditionally) → first subtest RED; restore → GREEN.

```bash
git add backend/internal/session_manager/
git commit -m "fix(session): retry a refused send once after clearing a stale exit (#114 effect 4)"
```

---

### Task 5: Daemon wiring, full verification, PR

**Files:**
- Modify: `backend/internal/daemon/daemon.go:258` (next to `lcStack.LCM.SetCompletionTerminator(sessMgr)`)
- Create: `docs/superpowers/plans/2026-08-23-stale-exit-liveness.md` (copy of this plan — repo convention)

**Interfaces:**
- Consumes: Task 1's `SetExitInspector`, Task 4's `SetStaleExitClearer` + `RuntimeExactInspector`.
- Produces: the wired system.

- [ ] **Step 1: Wire both edges**

At `daemon.go:258`, immediately after `lcStack.LCM.SetCompletionTerminator(sessMgr)`:

```go
	// #114 effects 3+4: lifecycle verifies a hook-reported exit against the
	// supervisor before believing it, and delivery heals a stale exited state.
	sessMgr.SetStaleExitClearer(lcStack.LCM)
	if insp, ok := sessMgr.RuntimeExactInspector(); ok {
		lcStack.LCM.SetExitInspector(insp)
	}
```

- [ ] **Step 2: Build and full backend suite**

```bash
cd backend
env $(env | grep '^AO_' | sed 's/=.*//' | sed 's/^/-u /' | tr '\n' ' ') go build ./...
env $(env | grep '^AO_' | sed 's/=.*//' | sed 's/^/-u /' | tr '\n' ' ') go test ./...
go vet ./internal/lifecycle/... ./internal/session_manager/... ./internal/daemon/...
```

Expected: build clean, suite green, vet clean. (The `env -u` dance clears ambient `AO_*`; if your shell has none, plain `go test ./...` is fine.)

- [ ] **Step 3: Copy the plan into the repo**

```bash
cp ~/.ao/briefs/plan-114-stale-exit-liveness.md docs/superpowers/plans/2026-08-23-stale-exit-liveness.md
git add docs/superpowers/plans/2026-08-23-stale-exit-liveness.md
git commit -m "docs: implementation plan for the #114 stale-exit liveness guard"
```

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin HEAD
gh pr create --base develop \
  --title "fix(lifecycle): verify liveness before believing a hook-reported exit (#114 effects 3+4)" \
  --body "<see below>"
```

PR body must include: which of the four #114 effects this addresses (3+4) and that 1+2 remain open on #114 by decision; the fail-open matrix (no inspector / probe error / missing metadata → today's behavior); why `process-exited` and the reaper are exempt (they are the authority); the probe-before-lock race and its backstops; the falsification runs per test (each guard reverted → red → restored → green); full-suite output summary. Reference #114 and #113/#116 as prior art. Do NOT write "fixes #114".

- [ ] **Step 5: Report**

Report to the orchestrator: PR number, CI state, and the exact test counts from the full-suite run.

---

## Self-Review (performed while writing)

- **Spec coverage:** effect 3 → Task 2 (gate); effect 4 → Tasks 3+4 (heal at delivery); fail-open non-negotiable → Task 1 matrix + Task 2/3 subtests; "not `process-exited`" exemption → Task 2 third test; "don't hold m.mu across a ps scan" → probe-before-lock in Tasks 2/3 with in-code comments; A2-preferred-plus-A1 → both shipped, one PR. Effects 1+2 deliberately absent (deferred on #114).
- **Placeholder scan:** the two "follow the neighboring code" notes (`working(...)` field shape, `sessionguard` success constant, `:4140` existing-condition closure) point at specific existing code the executor reads in-file — no invented symbols, no TBDs.
- **Type consistency:** `ClearStaleExit(ctx, id) (bool, error)` identical in Task 3 (producer) and Task 4 (consumer interface); `SetExitInspector` matches Task 5's wiring; `fakeExitInspector`/`supervisedRec`/`sessionState` defined in Task 1/2 before later use; `eventProcessExited` defined Task 1, used Task 2.

## Known risks the executor should not "fix"

- The probe race (agent dies between probe and write) is accepted; supervisor + reaper are the backstops — do not add locking around the probe.
- `ptyexec` runtimes don't implement the inspector → everything fails open there by design.
- A session re-poisoned between clear and retry yields a second refusal — bounded, correct, no loop.
