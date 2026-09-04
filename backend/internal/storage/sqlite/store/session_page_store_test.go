package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The paginated list is the archive's feed: newest first across projects,
// filtered in SQL, resumable from a keyset cursor over (updated_at, id). The
// unpaginated lists keep their (project_id, num) order for the CLI.
func TestListSessionsPageIsNewestFirstAndResumesFromCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	base := time.Unix(1700000000, 0).UTC()
	create := func(project string, minute int, terminated bool, kind domain.SessionKind) domain.SessionRecord {
		t.Helper()
		rec := sampleRecord(project)
		rec.Kind = kind
		rec.IsTerminated = terminated
		rec.CreatedAt = base.Add(time.Duration(minute) * time.Minute)
		rec.UpdatedAt = rec.CreatedAt
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatalf("create %s@%d: %v", project, minute, err)
		}
		return created
	}
	// Interleaved projects and states; minutes give the total recency order.
	a := create("p1", 1, true, domain.KindWorker)
	b := create("p2", 2, true, domain.KindOrchestrator)
	_ = create("p1", 3, false, domain.KindWorker) // active: never in a terminated page
	d := create("p2", 4, true, domain.KindWorker)
	e := create("p1", 5, true, domain.KindWorker)

	ids := func(recs []domain.SessionRecord) []domain.SessionID {
		out := make([]domain.SessionID, 0, len(recs))
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	terminated := true

	page1, err := s.ListSessionsPage(ctx, ports.SessionPageQuery{Terminated: &terminated, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(page1), []domain.SessionID{e.ID, d.ID}; !equalIDs(got, want) {
		t.Fatalf("page 1 = %v, want newest first %v", got, want)
	}

	last := page1[len(page1)-1]
	page2, err := s.ListSessionsPage(ctx, ports.SessionPageQuery{
		Terminated: &terminated, Limit: 2,
		Before: &ports.SessionPageCursor{UpdatedAt: last.UpdatedAt, ID: last.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(page2), []domain.SessionID{b.ID, a.ID}; !equalIDs(got, want) {
		t.Fatalf("page 2 = %v, want the rest in order %v", got, want)
	}

	byProject, err := s.ListSessionsPage(ctx, ports.SessionPageQuery{Project: "p1", Terminated: &terminated, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(byProject), []domain.SessionID{e.ID, a.ID}; !equalIDs(got, want) {
		t.Fatalf("p1 terminated = %v, want %v", got, want)
	}

	orchestrators, err := s.ListSessionsPage(ctx, ports.SessionPageQuery{OrchestratorOnly: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(orchestrators), []domain.SessionID{b.ID}; !equalIDs(got, want) {
		t.Fatalf("orchestrators = %v, want %v", got, want)
	}

	all, err := s.ListSessionsPage(ctx, ports.SessionPageQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("unfiltered page = %d rows, want all 5", len(all))
	}
}

func equalIDs(a, b []domain.SessionID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ = sqlite.Store{}
