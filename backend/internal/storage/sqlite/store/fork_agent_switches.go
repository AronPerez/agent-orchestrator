package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListForkAgentSwitches reads the retained table without restoring the old saga.
func (s *Store) ListForkAgentSwitches(ctx context.Context, id domain.SessionID) ([]domain.AgentSwitch, error) {
	rows, err := s.qr.ListForkAgentSwitches(ctx, string(id))
	return forkAgentSwitches(rows), err
}

// ListActiveForkAgentSwitches batches progress for the frozen UI session list.
func (s *Store) ListActiveForkAgentSwitches(ctx context.Context) ([]domain.AgentSwitch, error) {
	rows, err := s.qr.ListActiveForkAgentSwitches(ctx)
	return forkAgentSwitches(rows), err
}

// InsertForkAgentSwitch reserves a durable retry receipt before execution.
func (s *Store) InsertForkAgentSwitch(ctx context.Context, sw domain.AgentSwitch) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.InsertForkAgentSwitch(ctx, gen.InsertForkAgentSwitchParams{
		ID: sw.ID, SessionID: string(sw.SessionID), IdempotencyKey: sw.IdempotencyKey,
		RequestFingerprint: sw.RequestFingerprint, FromHarness: string(sw.FromHarness),
		TargetHarness: string(sw.TargetHarness), SourceGenerationID: sw.SourceGenerationID,
		RequestedAt: sw.RequestedAt, UpdatedAt: sw.UpdatedAt,
	})
}

// FinishForkAgentSwitch records an outcome without overwriting terminal history.
func (s *Store) FinishForkAgentSwitch(ctx context.Context, sw domain.AgentSwitch) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.FinishForkAgentSwitch(ctx, gen.FinishForkAgentSwitchParams{
		ID: sw.ID, State: sw.State, ErrorCode: sw.ErrorCode, UpdatedAt: sw.UpdatedAt,
	})
	if err == nil && n != 1 {
		return fmt.Errorf("fork switch %s changed concurrently", sw.ID)
	}
	return err
}

func forkAgentSwitches(rows []gen.AgentSwitch) []domain.AgentSwitch {
	out := make([]domain.AgentSwitch, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.AgentSwitch{
			ID: r.ID, SessionID: domain.SessionID(r.SessionID), FromHarness: domain.AgentHarness(r.FromHarness),
			TargetHarness: domain.AgentHarness(r.TargetHarness), TargetStartMode: r.TargetStartMode, State: r.State,
			AgentHandoffStatus: r.AgentHandoffStatus, SourceTranscriptStatus: r.SourceTranscriptStatus,
			SemanticHandoffIncluded: r.SemanticHandoffIncluded != 0, ErrorCode: r.ErrorCode,
			RequestedAt: r.RequestedAt, UpdatedAt: r.UpdatedAt, IdempotencyKey: r.IdempotencyKey,
			RequestFingerprint: r.RequestFingerprint, SourceGenerationID: r.SourceGenerationID,
			TargetGenerationID: r.TargetGenerationID, TargetRuntimeHandleID: r.TargetRuntimeHandleID,
		})
	}
	return out
}

// PurgeForkAgentSwitchReports acknowledges opt-out only after retained old-build
// reports are gone. The retired uploader is not part of provider switching.
func (s *Store) PurgeForkAgentSwitchReports(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "purge legacy switch reports", func(q *gen.Queries) error {
		if err := q.DisableForkAgentSwitchReporting(ctx); err != nil {
			return err
		}
		if err := q.PurgeForkAgentSwitchOutbox(ctx); err != nil {
			return err
		}
		if err := q.PurgeForkAgentSwitchReceipts(ctx); err != nil {
			return err
		}
		return q.PurgeForkAgentSwitchDeliveryState(ctx)
	})
}
