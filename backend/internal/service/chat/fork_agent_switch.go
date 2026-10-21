package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type forkSwitchStore interface {
	ListForkAgentSwitches(context.Context, domain.SessionID) ([]domain.AgentSwitch, error)
	InsertForkAgentSwitch(context.Context, domain.AgentSwitch) error
	FinishForkAgentSwitch(context.Context, domain.AgentSwitch) error
}

// ForkSwitchAgent translates the frozen UI protocol onto upstream switching.
// Admission returns 202 immediately; execution uses the manager's existing
// daemon-owned worker lifetime. No retired saga worker is revived.
func (s *Service) ForkSwitchAgent(ctx context.Context, id domain.SessionID, req domain.ProviderSwitchRequest) (domain.AgentSwitch, error) {
	unlock, err := s.lockProviderSwitch(ctx, id)
	if err != nil {
		return domain.AgentSwitch{}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			unlock()
		}
	}()
	runner, ok := s.terminalSwitcher.(interface {
		RunForkSwitch(func(context.Context)) error
	})
	if !ok {
		return domain.AgentSwitch{}, apierr.Unavailable("AGENT_SWITCH_UNAVAILABLE", "Switch worker is unavailable")
	}
	st, ok := s.store.(forkSwitchStore)
	if !ok {
		return domain.AgentSwitch{}, apierr.Unavailable("AGENT_SWITCH_UNAVAILABLE", "Switch history is unavailable")
	}
	rows, err := s.ForkListAgentSwitches(ctx, id)
	if err != nil {
		return domain.AgentSwitch{}, err
	}
	for _, sw := range rows {
		if req.IdempotencyKey != "" && req.IdempotencyKey == sw.IdempotencyKey {
			if !forkSwitchMatches(sw, req) {
				return domain.AgentSwitch{}, apierr.Conflict("AGENT_SWITCH_IDEMPOTENCY_CONFLICT", "Retry key was already used for another switch", nil)
			}
			return sw, nil
		}
	}
	for _, sw := range rows {
		if !sw.Terminal() {
			return domain.AgentSwitch{}, apierr.Conflict("AGENT_SWITCH_IN_PROGRESS", "Recover the previous switch before switching again", nil)
		}
	}
	rec, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return domain.AgentSwitch{}, err
	}
	if !found {
		return domain.AgentSwitch{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.IsTerminated {
		return domain.AgentSwitch{}, apierr.Conflict("SESSION_TERMINATED", "Restore the session before switching", nil)
	}
	if req.TargetHarness == rec.Harness {
		return domain.AgentSwitch{}, apierr.Conflict("AGENT_ALREADY_ACTIVE", "Target agent is already active", nil)
	}
	switch req.TargetHarness {
	case domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessFX:
	default:
		return domain.AgentSwitch{}, apierr.Invalid("UNSUPPORTED_AGENT", "Unsupported target agent", nil)
	}
	sw := domain.AgentSwitch{
		ID: "fork-switch-" + s.newID(), SessionID: id, FromHarness: rec.Harness, TargetHarness: req.TargetHarness,
		IdempotencyKey: req.IdempotencyKey, RequestFingerprint: forkSwitchFingerprint(id, req, false),
		SourceGenerationID: rec.Metadata.RuntimeLaunchID, RequestedAt: s.now(), UpdatedAt: s.now(),
		State: "preparing_handoff", AgentHandoffStatus: "not_attempted", SourceTranscriptStatus: "not_attempted",
	}
	if sw.IdempotencyKey == "" {
		sw.IdempotencyKey = sw.ID
	}
	// Chat ownership can have no terminal launch ID. This marker is never accepted
	// as a source-authored handoff fence or used to probe/stop a process.
	if sw.SourceGenerationID == "" {
		sw.SourceGenerationID = sw.ID
	}
	if err := st.InsertForkAgentSwitch(ctx, sw); err != nil {
		return domain.AgentSwitch{}, err
	}
	// Admission already holds the common provider lock; the worker releases it.
	if err := runner.RunForkSwitch(func(workCtx context.Context) {
		defer unlock()
		if err := s.runForkSwitch(workCtx, st, sw, req); err != nil {
			s.log.Error("persist legacy switch outcome", "session", id, "error", err)
		}
	}); err != nil {
		sw.State, sw.ErrorCode, sw.UpdatedAt = "failed", "request_cancelled", s.now()
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if saveErr := st.FinishForkAgentSwitch(saveCtx, sw); saveErr != nil {
			return domain.AgentSwitch{}, saveErr
		}
		return domain.AgentSwitch{}, err
	}
	handedOff = true
	return sw, nil
}

func (s *Service) runForkSwitch(ctx context.Context, st forkSwitchStore, sw domain.AgentSwitch, req domain.ProviderSwitchRequest) error {
	switchErr := s.switchProvider(ctx, sw.SessionID, req)
	sw.State, sw.ErrorCode = "completed", ""
	if switchErr != nil {
		s.log.Warn("legacy provider switch failed", "session", sw.SessionID, "error", switchErr)
		sw.State, sw.ErrorCode = "failed", "switch_failed"
		var refusal *domain.ProviderSwitchError
		if errors.As(switchErr, &refusal) && refusal.Kind == domain.ProviderSwitchRefusalRecoveryRequired || ctx.Err() != nil {
			sw.State, sw.ErrorCode = "stopping_source", "source_stop_unconfirmed"
		}
	} else {
		sw.TargetStartMode = "fresh"
	}
	sw.UpdatedAt = s.now()
	// Persist the outcome even when the HTTP deadline expired during shutdown.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := st.FinishForkAgentSwitch(saveCtx, sw); err != nil {
		return err
	}
	return nil
}

// ForkListAgentSwitches exposes legacy history without retry keys or local paths.
func (s *Service) ForkListAgentSwitches(ctx context.Context, id domain.SessionID) ([]domain.AgentSwitch, error) {
	rec, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found || rec.IsTaskPreparation {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	st, ok := s.store.(forkSwitchStore)
	if !ok {
		return nil, apierr.Unavailable("AGENT_SWITCH_UNAVAILABLE", "Switch history is unavailable")
	}
	rows, err := st.ListForkAgentSwitches(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i] = s.forkSwitchForRead(rows[i])
	}
	return rows, nil
}

// ForkRecoverAgentSwitch never replays the switch: it verifies or resumes the
// committed owner through the same manager used by upstream resume-agent.
func (s *Service) ForkRecoverAgentSwitch(ctx context.Context, id domain.SessionID, switchID string) (domain.AgentSwitch, error) {
	unlock, err := s.lockProviderSwitch(ctx, id)
	if err != nil {
		return domain.AgentSwitch{}, err
	}
	defer unlock()
	rows, err := s.ForkListAgentSwitches(ctx, id)
	if err != nil {
		return domain.AgentSwitch{}, err
	}
	for _, sw := range rows {
		if sw.ID != switchID {
			continue
		}
		if sw.Terminal() {
			return sw, nil
		}
		rec, found, err := s.sessions.GetSession(ctx, id)
		if err != nil {
			return domain.AgentSwitch{}, err
		}
		if !found {
			return domain.AgentSwitch{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
		}
		// Pre-upgrade sagas could launch a target out of band, before committing its
		// owner. Do not resume the source over that possibly-live target.
		if !strings.HasPrefix(sw.ID, "fork-switch-") && sw.TargetGenerationID != "" && (sw.TargetGenerationID != rec.Metadata.RuntimeLaunchID || rec.Harness != sw.TargetHarness) {
			return domain.AgentSwitch{}, apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Legacy target ownership is unconfirmed; stop the old target before restoring this session", nil)
		}
		if rec.Harness != sw.FromHarness && rec.Harness != sw.TargetHarness {
			return domain.AgentSwitch{}, apierr.Conflict("AGENT_SWITCH_RECOVERY_REQUIRED", "Session owner changed after this switch", nil)
		}
		recoverer, ok := s.terminalSwitcher.(interface {
			RecoverForkSwitchOwner(context.Context, domain.SessionID, domain.AgentHarness) error
		})
		if !ok {
			return domain.AgentSwitch{}, apierr.Unavailable("AGENT_SWITCH_UNAVAILABLE", "Agent recovery is unavailable")
		}
		if err := recoverer.RecoverForkSwitchOwner(ctx, id, rec.Harness); err != nil {
			return domain.AgentSwitch{}, err
		}
		sw.State, sw.ErrorCode = "failed", "switch_failed"
		if rec.Harness == sw.TargetHarness {
			sw.State, sw.ErrorCode = "completed", ""
			if sw.TargetStartMode == "" {
				sw.TargetStartMode = "fresh"
			}
		}
		if sw.AgentHandoffStatus == "requested" {
			sw.AgentHandoffStatus = "unavailable"
		}
		sw.UpdatedAt = s.now()
		st, ok := s.store.(forkSwitchStore)
		if !ok {
			return domain.AgentSwitch{}, apierr.Unavailable("AGENT_SWITCH_UNAVAILABLE", "Switch history is unavailable")
		}
		if err := st.FinishForkAgentSwitch(ctx, sw); err != nil {
			return domain.AgentSwitch{}, err
		}
		return sw, nil
	}
	return domain.AgentSwitch{}, apierr.NotFound("AGENT_SWITCH_NOT_FOUND", "Unknown agent switch")
}

func forkSwitchMatches(sw domain.AgentSwitch, req domain.ProviderSwitchRequest) bool {
	return sw.RequestFingerprint == forkSwitchFingerprint(sw.SessionID, req, false) ||
		req.Model == "" && sw.RequestFingerprint == forkSwitchFingerprint(sw.SessionID, req, true)
}

func forkSwitchFingerprint(id domain.SessionID, req domain.ProviderSwitchRequest, preModel bool) string {
	// Keep the old canonical field order, including its retired empty note field.
	payload := struct {
		SessionID     domain.SessionID    `json:"sessionId"`
		TargetHarness domain.AgentHarness `json:"targetHarness"`
		Note          string              `json:"note"`
		Model         string              `json:"model"`
	}{id, req.TargetHarness, "", strings.TrimSpace(req.Model)}
	raw, _ := json.Marshal(payload)
	if preModel {
		raw, _ = json.Marshal(struct {
			SessionID     domain.SessionID    `json:"sessionId"`
			TargetHarness domain.AgentHarness `json:"targetHarness"`
			Note          string              `json:"note"`
		}{id, req.TargetHarness, ""})
	}
	return fmt.Sprintf("v1:%x", sha256.Sum256(raw))
}

// ForkActiveAgentSwitches is batched so the session list keeps constant query cost.
func (s *Service) ForkActiveAgentSwitches(ctx context.Context) (map[domain.SessionID]*domain.AgentSwitch, error) {
	st, ok := s.store.(interface {
		ListActiveForkAgentSwitches(context.Context) ([]domain.AgentSwitch, error)
	})
	if !ok {
		return nil, nil
	}
	rows, err := st.ListActiveForkAgentSwitches(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.SessionID]*domain.AgentSwitch, len(rows))
	for _, sw := range rows {
		view := s.forkSwitchForRead(sw)
		out[sw.SessionID] = &view
	}
	return out, nil
}

func (s *Service) forkSwitchForRead(sw domain.AgentSwitch) domain.AgentSwitch {
	// A receipt left by a stopped daemon is recoverable, not an eternal spinner.
	value, _ := s.providerSwitches.Load(sw.SessionID)
	slot, running := value.(chan struct{})
	if !sw.Terminal() && (!running || len(slot) == 0) {
		sw.State, sw.ErrorCode = "stopping_source", "source_stop_unconfirmed"
	}
	return sw
}
