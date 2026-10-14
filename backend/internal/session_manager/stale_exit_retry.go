package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// StaleExitClearer is lifecycle.Manager's ClearStaleExit, late-bound here for
// the same cycle reason lifecycle late-binds its completionTerminator to this
// package (daemon assembly wires both, side by side).
type StaleExitClearer interface {
	ClearStaleExit(ctx context.Context, id domain.SessionID) (bool, error)
}

// SetStaleExitClearer wires lifecycle's stale-exit revival into delivery
// refusal. Wired once during daemon assembly, next to SetCompletionTerminator.
func (m *Manager) SetStaleExitClearer(c StaleExitClearer) {
	m.staleExitMu.Lock()
	defer m.staleExitMu.Unlock()
	m.staleExit = c
}

func (m *Manager) staleExitClearerRef() StaleExitClearer {
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

// deliverWithStaleExitRetry runs deliver once; if the guard refused because the
// session reads exited (#114: possibly a nested agent's lie), it asks lifecycle
// to verify-and-clear, and on a genuine clear retries exactly once.
//
// Every failure mode leaves today's behavior untouched: a messenger error is
// not a refusal and short-circuits before the clearer is consulted; an
// unwired clearer, a clearer error, and a refusal to clear all leave the
// original outcome standing. A session re-poisoned between clear and retry
// yields a second refusal -- bounded, and deliberately not a loop.
func deliverWithStaleExitRetry(ctx context.Context, id domain.SessionID, clearer StaleExitClearer,
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

// clearedStaleExit reports whether lifecycle just revived a session whose
// exited state was provably false. Any failure -- no clearer wired, a store
// error, or a genuinely dead agent -- answers false, leaving the caller's
// refusal exactly as it was before #114.
func (m *Manager) clearedStaleExit(ctx context.Context, id domain.SessionID) bool {
	clearer := m.staleExitClearerRef()
	if clearer == nil {
		return false
	}
	cleared, err := clearer.ClearStaleExit(ctx, id)
	return err == nil && cleared
}
