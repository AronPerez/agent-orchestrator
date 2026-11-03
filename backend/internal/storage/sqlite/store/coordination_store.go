package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ListSessionCoordination reads bounded evidence without changing delivery state.
func (s *Store) ListSessionCoordination(ctx context.Context, id domain.SessionID) ([]domain.CoordinationEvent, error) {
	messages, err := s.qr.ListSessionCoordinationMessages(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list coordination messages: %w", err)
	}
	reports, err := s.qr.ListSessionCoordinationReports(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list coordination reports: %w", err)
	}
	events := make([]domain.CoordinationEvent, 0, len(messages)+len(reports))
	for _, m := range messages {
		events = append(events, domain.CoordinationEvent{ID: "message:" + m.ID, Kind: "message", SourceSessionID: m.SenderSessionID, TargetSessionID: string(*m.TargetSessionID), Summary: m.Summary, State: "recorded", CreatedAt: m.CreatedAt})
	}
	for _, r := range reports {
		events = append(events, domain.CoordinationEvent{ID: "report:" + r.ID, Kind: "report", SourceSessionID: r.SessionID, Summary: r.Summary, State: r.DeliveryState, ReportState: r.State, CreatedAt: r.CreatedAt})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].ID > events[j].ID
		}
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})
	if len(events) > 51 {
		events = events[:51]
	}
	return events, nil
}
