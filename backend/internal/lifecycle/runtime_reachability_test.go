package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A failed probe still writes no durable fact — it is not proof of death — but
// it must remain readable, or every surface keeps reporting a confident status
// for a session whose runtime AO cannot reach.
func TestRuntimeUnreachableTracksTheLatestProbe(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now()
	st.sessions[rec.ID] = rec

	if m.RuntimeUnreachable(rec.ID) {
		t.Fatal("unreachable before any observation")
	}

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{Runtime: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if !m.RuntimeUnreachable(rec.ID) {
		t.Fatal("failed probe did not mark the runtime unreachable")
	}
	if got := st.sessions[rec.ID]; got.IsTerminated || got.Activity.State == domain.ActivityExited {
		t.Fatalf("failed probe must not be treated as death, got %+v", got)
	}

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{Runtime: ports.ProbeAlive}); err != nil {
		t.Fatal(err)
	}
	if m.RuntimeUnreachable(rec.ID) {
		t.Fatal("a reachable probe must clear the flag")
	}
}

// A terminated session is no longer probed, so a leftover flag would outlive
// the fact it reported.
func TestRuntimeUnreachableClearsWhenSessionTerminates(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	st.sessions[rec.ID] = rec
	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{Runtime: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if !m.RuntimeUnreachable(rec.ID) {
		t.Fatal("setup: expected unreachable")
	}

	terminated := st.sessions[rec.ID]
	terminated.IsTerminated = true
	st.sessions[rec.ID] = terminated
	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{Runtime: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if m.RuntimeUnreachable(rec.ID) {
		t.Fatal("terminated session kept a stale unreachable flag")
	}
}
