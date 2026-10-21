package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RecoverForkSwitchOwner is the frozen UI's recovery adapter. Never infer death
// from a failed probe, and never relaunch a different owner after a racing switch.
func (m *Manager) RecoverForkSwitchOwner(ctx context.Context, id domain.SessionID, expected domain.AgentHarness) error {
	if err := m.beginAgentOperation(ctx, id, agentOperationProviderSwitch); err != nil {
		return err
	}
	reserved := true
	defer func() {
		if reserved {
			m.endAgentOperation(id, agentOperationProviderSwitch)
		}
	}()
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.IsTerminated || rec.Harness != expected {
		return apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Session owner changed or was terminated", nil)
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat != nil && m.chat.HasLiveChatController(id) {
			return nil
		}
	} else {
		if m.launchedTUIAgentStillAlive(ctx, rec) {
			return nil
		}
		// A terminal pane existing is not proof of the expected agent. It might be
		// a shell or an unconfirmed target, so leave it alone unless death is proven.
		alive, err := m.runtime.IsAlive(ctx, runtimeHandle(rec.Metadata))
		if err != nil || alive {
			return apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Cannot confirm the old terminal stopped", nil)
		}
		revision := rec.Revision
		if err := m.lcm.ApplyActivitySignal(ctx, id, ports.ActivitySignal{
			Valid: true, State: domain.ActivityExited, Event: "process-exited",
			LaunchID: rec.Metadata.RuntimeLaunchID, ExpectedRevision: &revision, Timestamp: m.clock(),
		}); err != nil {
			return err
		}
	}
	// Resume owns the same operation gate. Its under-lock owner fence covers the
	// gap between releasing this probe reservation and acquiring resume's.
	m.endAgentOperation(id, agentOperationProviderSwitch)
	reserved = false
	_, err = m.resumeAgentWithExpectedOwner(ctx, id, false, expected)
	return err
}

// RunForkSwitch shares deferred recovery's shutdown fence and worker lifetime.
func (m *Manager) RunForkSwitch(work func(context.Context)) error {
	if err := m.beginAgentSwitchAttempt(); err != nil {
		return err
	}
	go func() {
		defer m.agentSwitchWorkers.Done()
		work(m.backgroundContext)
	}()
	return nil
}
