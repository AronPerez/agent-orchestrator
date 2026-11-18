package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionOverlapNoticesRoundTripAndScopeToProject(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	seedProject(t, store, "other")
	first, err := store.CreateSession(ctx, sampleRecord("demo"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSession(ctx, sampleRecord("demo"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateSession(ctx, sampleRecord("other"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	for _, notice := range []domain.SessionOverlapNotice{
		{SessionID: first.ID, PeerSessionID: second.ID, NotifiedAt: now},
		{SessionID: second.ID, PeerSessionID: first.ID, NotifiedAt: now.Add(time.Second)},
		{SessionID: other.ID, PeerSessionID: first.ID, NotifiedAt: now.Add(2 * time.Second)},
	} {
		if err := store.UpsertSessionOverlapNotice(ctx, notice); err != nil {
			t.Fatalf("upsert %+v: %v", notice, err)
		}
	}

	got, err := store.ListProjectSessionOverlapNotices(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SessionID != first.ID || got[0].PeerSessionID != second.ID ||
		got[1].SessionID != second.ID || got[1].PeerSessionID != first.ID {
		t.Fatalf("demo notices = %+v", got)
	}
	if err := store.DeleteSessionOverlapNotice(ctx, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.ListProjectSessionOverlapNotices(ctx, "demo")
	if err != nil || len(got) != 1 || got[0].SessionID != second.ID {
		t.Fatalf("notices after delete = %+v, %v", got, err)
	}

	second.IsTerminated = true
	if err := store.UpdateSession(ctx, second); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteTerminatedSession(ctx, second.ID); err != nil || !deleted {
		t.Fatalf("delete terminated session: deleted=%v err=%v", deleted, err)
	}
	got, err = store.ListProjectSessionOverlapNotices(ctx, "demo")
	if err != nil || len(got) != 0 {
		t.Fatalf("notices after session cascade = %+v, %v", got, err)
	}
}
