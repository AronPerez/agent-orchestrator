package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type coordinationReader interface {
	ListSessionCoordination(context.Context, domain.SessionID) ([]domain.CoordinationEvent, error)
}

// Coordination returns recorded communication, not transcript-derived guesses.
func (s *Service) Coordination(ctx context.Context, id domain.SessionID) ([]domain.CoordinationEvent, error) {
	_, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load coordination session: %w", err)
	}
	if !found {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	reader, ok := s.store.(coordinationReader)
	if !ok {
		return nil, apierr.NotImplemented("COORDINATION_UNAVAILABLE", "Coordination history is unavailable")
	}
	return reader.ListSessionCoordination(ctx, id)
}
