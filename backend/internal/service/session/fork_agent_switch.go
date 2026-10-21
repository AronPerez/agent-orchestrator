package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SetForkAgentSwitchReader preserves the frozen UI snapshot alongside its history API.
func (s *Service) SetForkAgentSwitchReader(read func(context.Context) (map[domain.SessionID]*domain.AgentSwitch, error)) {
	s.forkSwitches = read
}
