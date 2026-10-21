package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ResolveForkSwitchConfig preserves the old dialog's omitted-model inheritance.
func (m *Manager) ResolveForkSwitchConfig(ctx context.Context, rec domain.SessionRecord, target domain.AgentHarness, model string) (ports.AgentConfig, error) {
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return ports.AgentConfig{}, err
	}
	return m.resolveAgentConfig(ctx, ports.SpawnConfig{
		ProjectID: rec.ProjectID, Kind: rec.Kind, Harness: target,
		AgentConfig: ports.AgentConfig{Model: model},
	}, project.Config)
}

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
	preLaunch := false
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat != nil && m.chat.HasLiveChatController(id) {
			return nil
		}
	} else {
		if m.launchedTUIAgentStillAlive(ctx, rec) {
			return nil
		}
		if rec.Metadata.RuntimeHandleID == "" {
			// A saved note with no launch intent is the atomic target-owner commit.
			// Once launch intent exists, a runtime may be alive but not yet committed.
			if rec.Metadata.RuntimeLaunchID != "" || rec.Metadata.AgentSessionID != "" || m.sessionLaunchOptions(ctx, id).HandoffNote == "" {
				return apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Target launch ownership is unconfirmed", nil)
			}
			preLaunch = true
		} else {
			// A terminal pane may be a shell or an unconfirmed target, not this owner.
			alive, err := m.runtime.IsAlive(ctx, runtimeHandle(rec.Metadata))
			if err != nil || alive {
				return apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Cannot confirm the old terminal stopped", nil)
			}
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
	_, err = m.resumeAgentWithExpectedOwner(ctx, id, preLaunch, false, expected)
	return err
}

// reserveForkSwitchLaunch distinguishes a pre-launch crash from an uncommitted
// runtime. Recovery must not launch over the latter, even with no saved handle.
func (m *Manager) reserveForkSwitchLaunch(ctx context.Context, id domain.SessionID, expected domain.AgentHarness, launchID string) error {
	store, ok := m.store.(interface {
		ReserveForkSwitchLaunch(context.Context, domain.SessionRecord, string) (bool, error)
	})
	if !ok {
		return apierr.Unavailable("AGENT_SWITCH_UNAVAILABLE", "Switch launch reservation is unavailable")
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return err
	}
	if rec.Harness != expected || rec.Metadata.RuntimeHandleID != "" {
		return apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Session owner changed before launch", nil)
	}
	rec.UpdatedAt = m.clock()
	reserved, err := store.ReserveForkSwitchLaunch(ctx, rec, launchID)
	if err != nil {
		return err
	}
	if !reserved {
		return apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Session changed before launch", nil)
	}
	return nil
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
