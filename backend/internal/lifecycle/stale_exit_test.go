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
