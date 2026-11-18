package decision

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDecisionBoardSnapshot(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	mine, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := fixture.service.Raise(ctx, Actor{Session: fixture.w2}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	early, err := fixture.service.Raise(ctx, Actor{Session: fixture.epic}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	theirs, err = fixture.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, theirs.ID, AnswerInput{Choice: string(domain.DecisionChoiceOption), Option: 1})
	if err != nil {
		t.Fatal(err)
	}
	done, err := fixture.service.Raise(ctx, Actor{Session: fixture.w2}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	done, err = fixture.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, done.ID, AnswerInput{Choice: string(domain.DecisionChoiceOption), Option: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w2}, done.ID, OutcomeInput{ForEntry: done.StandingEntryID, Text: "Shipped"}); err != nil {
		t.Fatal(err)
	}

	// A board read in local time still reports a UTC cutoff exactly seven
	// days before the injected clock.
	boardAt := time.Date(2026, 11, 18, 6, 30, 0, 0, time.FixedZone("CST", -6*60*60))
	fixture.service.now = func() time.Time { return boardAt }
	board, err := fixture.service.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !board.DoneSince.Equal(boardAt.Add(-DecisionBoardDoneWindow)) || board.DoneSince.Location() != time.UTC {
		t.Fatalf("DoneSince = %v, want %v in UTC", board.DoneSince, boardAt.Add(-DecisionBoardDoneWindow).UTC())
	}
	if board.ServingHostID != "h_a" {
		t.Fatalf("ServingHostID = %q, want h_a", board.ServingHostID)
	}
	if board.NeedsYouCount != 2 {
		t.Fatalf("NeedsYouCount = %d, want 2", board.NeedsYouCount)
	}
	// The fixture clock writes everything in 1970, so the Done call is outside
	// this window; active calls are always present.
	if got, want := callIDs(board.Calls), mine.ID+","+theirs.ID+","+early.ID; got != want {
		t.Fatalf("board = %q, want %q", got, want)
	}
	for _, call := range board.Calls {
		if call.ID == early.ID && (call.Raised.StreamSessionID != fixture.epic || call.Raised.StreamName != "Checkout epic") {
			t.Fatalf("early manager call stream = %q/%q, want the epic stream", call.Raised.StreamSessionID, call.Raised.StreamName)
		}
	}

	fixture.service.now = func() time.Time { return time.UnixMilli(1_000_000).UTC().Add(time.Hour) }
	board, err = fixture.service.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := callIDs(board.Calls), mine.ID+","+theirs.ID+","+early.ID+","+done.ID; got != want {
		t.Fatalf("board inside the Done window = %q, want %q", got, want)
	}
	if board.NeedsYouCount != 2 {
		t.Fatalf("NeedsYouCount with Done present = %d, want 2", board.NeedsYouCount)
	}
}

func TestDecisionBoardEmpty(t *testing.T) {
	fixture := newDecisionService(t)
	board, err := fixture.service.Board(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if board.Calls == nil || len(board.Calls) != 0 || board.NeedsYouCount != 0 {
		t.Fatalf("empty board = %+v, want a non-nil empty call list", board)
	}
}

func TestDecisionSearchLimit(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	for range 25 {
		if _, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		limit, want int
	}{{0, DefaultDecisionSearchLimit}, {-1, DefaultDecisionSearchLimit}, {3, 3}, {25, 25}, {500, MaxDecisionSearchLimit}} {
		calls, err := fixture.service.Search(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, "auth scheme", tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		want := min(tc.want, 25)
		if len(calls) != want {
			t.Fatalf("Search(limit=%d) returned %d calls, want %d", tc.limit, len(calls), want)
		}
	}
}
