package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

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
		wantClears   int
	}{
		{"exited + cleared -> retried once, delivered",
			[]sessionguard.Outcome{sessionguard.SuppressedExited, sessionguard.Sent},
			&fakeClearer{cleared: true}, sessionguard.Sent, 2, 1},
		{"exited + not cleared (real exit) -> refusal stands",
			[]sessionguard.Outcome{sessionguard.SuppressedExited},
			&fakeClearer{cleared: false}, sessionguard.SuppressedExited, 1, 1},
		{"exited + clearer errors -> refusal stands",
			[]sessionguard.Outcome{sessionguard.SuppressedExited},
			&fakeClearer{cleared: true, err: errors.New("store down")}, sessionguard.SuppressedExited, 1, 1},
		{"exited + no clearer wired -> refusal stands",
			[]sessionguard.Outcome{sessionguard.SuppressedExited},
			nil, sessionguard.SuppressedExited, 1, 0},
		{"still exited after clear -> second refusal stands, no third try",
			[]sessionguard.Outcome{sessionguard.SuppressedExited, sessionguard.SuppressedExited},
			&fakeClearer{cleared: true}, sessionguard.SuppressedExited, 2, 1},
		{"non-exited outcome -> untouched, clearer never called",
			[]sessionguard.Outcome{sessionguard.SuppressedTerminated},
			&fakeClearer{cleared: true}, sessionguard.SuppressedTerminated, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivers := 0
			deliver := func() (sessionguard.Outcome, error) {
				out := tc.outcomes[delivers]
				delivers++
				return out, nil
			}
			var clearer StaleExitClearer
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
			if tc.clearer != nil && tc.clearer.calls != tc.wantClears {
				t.Fatalf("clearer called %d times, want %d", tc.clearer.calls, tc.wantClears)
			}
		})
	}
}

// A messenger error short-circuits before the clearer is ever consulted: an
// error is not a refusal, and today's error path must stay identical.
func TestDeliverWithStaleExitRetryPassesErrorsThrough(t *testing.T) {
	clearer := &fakeClearer{cleared: true}
	want := errors.New("pane write failed")
	out, err := deliverWithStaleExitRetry(context.Background(), "s1", clearer,
		func() (sessionguard.Outcome, error) { return sessionguard.SuppressedExited, want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if out != sessionguard.SuppressedExited {
		t.Fatalf("outcome = %v, want it passed through untouched", out)
	}
	if clearer.calls != 0 {
		t.Fatalf("clearer consulted on an error path (%d calls)", clearer.calls)
	}
}

// healingClearer flips the store row idle the first time it is asked, so the
// readiness loop can observe a real heal.
type healingClearer struct {
	st    *fakeStore
	id    domain.SessionID
	calls int
	heal  bool
}

func (h *healingClearer) ClearStaleExit(context.Context, domain.SessionID) (bool, error) {
	h.calls++
	if !h.heal {
		return false, nil
	}
	rec := h.st.sessions[h.id]
	rec.Activity = domain.Activity{State: domain.ActivityIdle}
	h.st.sessions[h.id] = rec
	return true, nil
}

func exitedSession(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, ProjectID: "ao", Harness: domain.HarnessCodex,
		Mode:          domain.SessionModeChat,
		Activity:      domain.Activity{State: domain.ActivityExited},
		FirstSignalAt: time.Now(),
		Metadata:      domain.SessionMetadata{RuntimeHandleID: "h", RuntimeLaunchID: "l"},
	}
}

// #114 effect 4 at the readiness gate: a session whose exited state is a nested
// agent's lie heals and the send proceeds instead of refusing AGENT_EXITED.
func TestWaitForMessageDeliveryReadyHealsStaleExit(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = exitedSession("s1")
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: fakeAgent{}}, Store: st})
	clearer := &healingClearer{st: st, id: "s1", heal: true}
	m.SetStaleExitClearer(clearer)

	if err := m.WaitForMessageDeliveryReady(context.Background(), "s1"); err != nil {
		t.Fatalf("WaitForMessageDeliveryReady = %v, want the healed session to proceed", err)
	}
	if clearer.calls != 1 {
		t.Fatalf("clearer called %d times, want exactly 1", clearer.calls)
	}
}

// A genuinely exited agent still refuses, and is verified at most once.
func TestWaitForMessageDeliveryReadyStillRefusesRealExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		clearer *healingClearer // nil = not wired
		want    int
	}{
		{"clearer says the agent is really gone", &healingClearer{heal: false}, 1},
		{"no clearer wired", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s1"] = exitedSession("s1")
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: fakeAgent{}}, Store: st})
			if tc.clearer != nil {
				tc.clearer.st, tc.clearer.id = st, "s1"
				m.SetStaleExitClearer(tc.clearer)
			}
			if err := m.WaitForMessageDeliveryReady(context.Background(), "s1"); !errors.Is(err, ErrAgentExited) {
				t.Fatalf("err = %v, want ErrAgentExited", err)
			}
			if tc.clearer != nil && tc.clearer.calls != tc.want {
				t.Fatalf("clearer called %d times, want %d (verify at most once per call)", tc.clearer.calls, tc.want)
			}
		})
	}
}
