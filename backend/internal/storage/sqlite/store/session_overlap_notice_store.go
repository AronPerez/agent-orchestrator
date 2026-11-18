package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListProjectSessionOverlapNotices returns the directed collision notices for
// sessions in one project.
func (s *Store) ListProjectSessionOverlapNotices(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionOverlapNotice, error) {
	rows, err := s.qr.ListProjectSessionOverlapNotices(ctx, &projectID)
	if err != nil {
		return nil, fmt.Errorf("list overlap notices for project %s: %w", projectID, err)
	}
	result := make([]domain.SessionOverlapNotice, 0, len(rows))
	for _, row := range rows {
		result = append(result, domain.SessionOverlapNotice{
			SessionID:     domain.SessionID(row.SessionID),
			PeerSessionID: domain.SessionID(row.PeerSessionID),
			NotifiedAt:    row.NotifiedAt,
		})
	}
	return result, nil
}

// UpsertSessionOverlapNotice marks one recipient/peer overlap as delivered.
func (s *Store) UpsertSessionOverlapNotice(ctx context.Context, notice domain.SessionOverlapNotice) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSessionOverlapNotice(ctx, gen.UpsertSessionOverlapNoticeParams{
		SessionID:     string(notice.SessionID),
		PeerSessionID: string(notice.PeerSessionID),
		NotifiedAt:    notice.NotifiedAt,
	}); err != nil {
		return fmt.Errorf("upsert overlap notice %s -> %s: %w", notice.SessionID, notice.PeerSessionID, err)
	}
	return nil
}

// DeleteSessionOverlapNotice re-arms a later collision after this pair clears.
func (s *Store) DeleteSessionOverlapNotice(ctx context.Context, sessionID, peerSessionID domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSessionOverlapNotice(ctx, gen.DeleteSessionOverlapNoticeParams{
		SessionID:     string(sessionID),
		PeerSessionID: string(peerSessionID),
	}); err != nil {
		return fmt.Errorf("delete overlap notice %s -> %s: %w", sessionID, peerSessionID, err)
	}
	return nil
}
