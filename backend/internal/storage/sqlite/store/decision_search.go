package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Hand-written because sqlc cannot parse FTS5 MATCH or rowid.
const searchDecisionCallsSQL = `
SELECT decision_calls.body
FROM decision_calls_fts
JOIN decision_calls ON decision_calls.rowid = decision_calls_fts.rowid
WHERE decision_calls_fts MATCH ?
ORDER BY
    CASE WHEN ? = 1 AND decision_calls.project_id = ? THEN 0 ELSE 1 END,
    decision_calls.retired,
    decision_calls_fts.rank,
    decision_calls_fts.rowid
LIMIT ?`

// SearchDecisionCalls returns matching calls by FTS rank. All words must match;
// when none do, a multi-word query falls back to any word.
func (s *Store) SearchDecisionCalls(ctx context.Context, query string, project domain.ProjectID, preferProject bool, limit int) ([]domain.DecisionCall, error) {
	words := ftsWords(strings.Join(domain.SearchTerms(query), " "))
	if len(words) == 0 {
		return nil, nil
	}
	terms := make([]string, len(words))
	for i, word := range words {
		terms[i] = `"` + word + `"`
	}
	terms[len(terms)-1] += "*"
	calls, err := s.searchDecisionCalls(ctx, strings.Join(terms, " "), project, preferProject, limit)
	if err != nil || len(calls) > 0 || len(terms) == 1 {
		return calls, err
	}
	return s.searchDecisionCalls(ctx, strings.Join(terms, " OR "), project, preferProject, limit)
}

func (s *Store) searchDecisionCalls(ctx context.Context, match string, project domain.ProjectID, preferProject bool, limit int) ([]domain.DecisionCall, error) {
	preference := 0
	if preferProject {
		preference = 1
	}
	rows, err := s.readDB.QueryContext(ctx, searchDecisionCallsSQL, match, preference, string(project), limit)
	if err != nil {
		return nil, fmt.Errorf("search decision calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	calls := make([]domain.DecisionCall, 0)
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("scan decision call search result: %w", err)
		}
		var call domain.DecisionCall
		if err := json.Unmarshal([]byte(body), &call); err != nil {
			return nil, fmt.Errorf("decode decision call search result: %w", err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search decision calls: %w", err)
	}
	return calls, nil
}
