package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeExitInspector records probe calls; used by every stale-exit test here.
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

func supervisedRec(id domain.SessionID) domain.SessionRecord {
	rec := working(id)
	rec.Metadata.RuntimeHandleID = "handle-1"
	rec.Metadata.RuntimeLaunchID = "launch-1"
	return rec
}

// agentProvablyAlive answers true ONLY on a wired inspector returning
// (true, nil) for a record carrying both handle and launch ids. Every other
// combination fails open (false -> the exit is applied as today).
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
		{"probe errors - fail open", &fakeExitInspector{alive: true, err: errors.New("ps failed")}},
		{"no inspector wired - fail open", nil},
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

// AO's own authorities are never probed. The supervisor's process-exited report
// ran wait4 on the agent; the chat controller's stopped report observed its own
// provider stream end and is already fenced by ControllerGeneration. Both ARE
// the authority, so gating them behind a liveness probe could leave a session
// that can never be marked exited -- the inverse bug, and the worse one.
func TestAuthoritativeExitNeverProbed(t *testing.T) {
	for _, event := range []string{"process-exited", "chat.controller.stopped"} {
		t.Run(event, func(t *testing.T) {
			store := newFakeAgentSwitchLifecycleStore()
			rec := supervisedRec("s1")
			store.setSession(rec)
			insp := &fakeExitInspector{alive: true} // even a live-looking probe must not matter
			m := New(store, &fakeMessenger{})
			m.SetExitInspector(insp)

			if err := m.ApplyActivitySignal(ctx, rec.ID, exitSignal(event)); err != nil {
				t.Fatalf("ApplyActivitySignal: %v", err)
			}
			if got := sessionState(t, store, rec.ID); got != domain.ActivityExited {
				t.Fatalf("%s exit not applied: state=%s", event, got)
			}
			if insp.callCount() != 0 {
				t.Fatalf("%s was probed %d times; it is the authority", event, insp.callCount())
			}
		})
	}
}
