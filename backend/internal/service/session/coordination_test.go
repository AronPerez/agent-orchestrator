package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type coordinationTestStore struct {
	*fakeStore
	called bool
	err    error
}

func (s *coordinationTestStore) ListSessionCoordination(context.Context, domain.SessionID) ([]domain.CoordinationEvent, error) {
	s.called = true
	return []domain.CoordinationEvent{{ID: "recorded"}}, s.err
}
func TestCoordinationRequiresExistingSession(t *testing.T) {
	store := &coordinationTestStore{fakeStore: newFakeStore()}
	svc := &Service{store: store}
	_, err := svc.Coordination(context.Background(), "missing")
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != "SESSION_NOT_FOUND" || store.called {
		t.Fatalf("err=%v read=%v", err, store.called)
	}
	store.sessions["known"] = domain.SessionRecord{ID: "known"}
	events, err := svc.Coordination(context.Background(), "known")
	if err != nil || !store.called || len(events) != 1 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	store.err = errors.New("read unavailable")
	if _, err := svc.Coordination(context.Background(), "known"); !errors.Is(err, store.err) {
		t.Fatalf("err=%v", err)
	}
}
