package decision

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// MaxDecisionLookupIDs bounds one id lookup.
const MaxDecisionLookupIDs = 100

// Lookup returns the calls this machine holds among ids, in the order asked,
// each once; unknown ids are left out. The desktop uses it to prefer this
// computer's state over a connected host's copy of the same call.
func (s *Service) Lookup(ctx context.Context, ids []string) ([]domain.DecisionCall, error) {
	if len(ids) > MaxDecisionLookupIDs {
		return nil, apierr.Invalid("DECISION_IDS_TOO_MANY",
			fmt.Sprintf("Look up at most %d calls at a time.", MaxDecisionLookupIDs), nil)
	}
	seen := make(map[string]bool, len(ids))
	calls := make([]domain.DecisionCall, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		call, found, err := s.store.GetDecisionCall(ctx, id)
		if err != nil {
			return nil, err
		}
		if found {
			calls = append(calls, call)
		}
	}
	return calls, nil
}
