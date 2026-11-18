package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func decisionRaised(entryID, origin, callID string, owner, stream domain.SessionID, streamName string, writtenAt time.Time) domain.DecisionEntry {
	payload := domain.DecisionRaisedPayload{
		Title:   "Which auth scheme should the proxy use?",
		Context: "The connector calls the proxy for each tenant.",
		Options: []domain.DecisionOption{
			{Label: "OAuth client credentials", Consequence: "One app registration per tenant.", Recommended: true},
			{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand."},
		},
		ProjectID:        "shop",
		ProjectName:      "Shop",
		MachineLabel:     "mac-mini",
		OwnerSessionID:   owner,
		OwnerSessionName: string(owner),
		StreamSessionID:  stream,
		StreamName:       streamName,
	}
	return domain.DecisionEntry{
		ID: entryID, Origin: origin, CallID: callID, Kind: domain.DecisionKindRaised,
		Payload: mustDecisionJSON(payload), WrittenAt: writtenAt,
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: owner,
	}
}

func decisionAnswered(entryID, origin, callID string, option int, writtenAt time.Time) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: entryID, Origin: origin, CallID: callID, Kind: domain.DecisionKindAnswered,
		Payload:   mustDecisionJSON(domain.DecisionAnsweredPayload{Choice: domain.DecisionChoiceOption, Option: option}),
		WrittenAt: writtenAt, AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceCLI,
	}
}

func decisionDelivered(entryID, origin, callID, forEntry string, session domain.SessionID, writtenAt time.Time) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: entryID, Origin: origin, CallID: callID, Kind: domain.DecisionKindDelivered,
		Payload:   mustDecisionJSON(domain.DecisionDeliveredPayload{ForEntry: forEntry, Status: domain.DecisionDeliveredSent, SessionID: session}),
		WrittenAt: writtenAt, AuthorKind: domain.DecisionAuthorSystem,
	}
}

func mustDecisionJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func appendDecision(t *testing.T, store *sqlite.Store, entry domain.DecisionEntry, now time.Time) (domain.DecisionEntry, domain.DecisionCall) {
	t.Helper()
	stored, call, err := store.AppendDecisionEntry(context.Background(), entry, now)
	if err != nil {
		t.Fatal(err)
	}
	return stored, call
}

func TestAppendDecisionEntryAssignsSequenceAndStamp(t *testing.T) {
	store := newTestStore(t)
	at := time.UnixMilli(1_000_000).UTC()

	first, _ := appendDecision(t, store, decisionRaised("dent_1", "h_a", "dec_1", "w-1", "", "", at), at)
	if first.OriginSeq != 1 || first.Stamp != (domain.DecisionStamp{WallMS: 1_000_000}) {
		t.Fatalf("first = %+v", first)
	}
	second, _ := appendDecision(t, store, decisionAnswered("dent_2", "h_a", "dec_1", 1, at.Add(time.Second)), at.Add(-time.Minute))
	if second.OriginSeq != 2 || second.Stamp != (domain.DecisionStamp{WallMS: 1_000_000, Counter: 1}) {
		t.Fatalf("second = %+v", second)
	}
	other, _ := appendDecision(t, store, decisionRaised("dent_3", "h_b", "dec_2", "w-9", "", "", at.Add(time.Second)), at.Add(time.Second))
	if other.OriginSeq != 1 || other.Stamp != (domain.DecisionStamp{WallMS: 1_001_000}) {
		t.Fatalf("other = %+v", other)
	}
}

func TestAppendDecisionEntryRecomputesTheCall(t *testing.T) {
	store := newTestStore(t)
	at := time.UnixMilli(1_000_000).UTC()
	raisedEntry := decisionRaised("dent_1", "h_a", "dec_1", "w-1", "", "", at)
	_, raised := appendDecision(t, store, raisedEntry, at)
	if raised.State != domain.DecisionStateNeedsYou || !raised.RaisedAt.Equal(raisedEntry.WrittenAt) {
		t.Fatalf("raised call = %+v", raised)
	}
	_, answered := appendDecision(t, store, decisionAnswered("dent_2", "h_a", "dec_1", 2, at.Add(time.Second)), at.Add(time.Second))
	if answered.State != domain.DecisionStateInProgress || answered.Option != 2 {
		t.Fatalf("answered call = %+v", answered)
	}
	got, ok, err := store.GetDecisionCall(context.Background(), "dec_1")
	if err != nil || !ok || !reflect.DeepEqual(got, answered) {
		t.Fatalf("GetDecisionCall() = %+v, %v, %v; want %+v", got, ok, err, answered)
	}
	if _, ok, err := store.GetDecisionCall(context.Background(), "dec_missing"); err != nil || ok {
		t.Fatalf("missing call: ok=%v err=%v", ok, err)
	}
}

func TestDecisionEntriesKeepUnknownKindsByteForByte(t *testing.T) {
	store := newTestStore(t)
	at := time.UnixMilli(1_000_000).UTC()
	appendDecision(t, store, decisionRaised("dent_1", "h_a", "dec_1", "w-1", "", "", at), at)
	raw := json.RawMessage(`{"question":"why", "future" : [1,2]}`)
	_, call := appendDecision(t, store, domain.DecisionEntry{
		ID: "dent_2", Origin: "h_a", CallID: "dec_1", Kind: "asked", Payload: raw,
		WrittenAt: at.Add(time.Second), AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceCLI,
	}, at.Add(time.Second))
	if call.State != domain.DecisionStateNeedsYou {
		t.Fatalf("state = %q", call.State)
	}
	entries, err := store.ListDecisionEntries(context.Background(), "dec_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Kind != "asked" || string(entries[1].Payload) != string(raw) {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestAppendDecisionEntryRefusesAnIncompleteEntry(t *testing.T) {
	store := newTestStore(t)
	_, _, err := store.AppendDecisionEntry(context.Background(), domain.DecisionEntry{
		ID: "dent_1", CallID: "dec_1", Kind: domain.DecisionKindRaised,
	}, time.Now())
	if !errors.Is(err, domain.ErrDecisionEntryInvalid) {
		t.Fatalf("error = %v, want ErrDecisionEntryInvalid", err)
	}
}

func TestDecisionCallLists(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.UnixMilli(1_000_000).UTC()
	entries := []domain.DecisionEntry{
		decisionRaised("dent_1", "h_a", "dec_1", "w-1", "epic", "Checkout", at),
		decisionRaised("dent_2", "h_a", "dec_2", "w-1", "epic", "Checkout epic", at.Add(time.Second)),
		decisionRaised("dent_3", "h_a", "dec_3", "w-2", "", "", at.Add(2*time.Second)),
		decisionRaised("dent_4", "h_b", "dec_4", "w-1", "", "", at.Add(3*time.Second)),
		decisionAnswered("dent_5", "h_a", "dec_1", 1, at.Add(4*time.Second)),
		decisionAnswered("dent_6", "h_a", "dec_4", 1, at.Add(5*time.Second)),
	}
	for i, entry := range entries {
		appendDecision(t, store, entry, at.Add(time.Duration(i)*time.Second))
	}

	owner, err := store.ListDecisionCallsForSession(ctx, "h_a", "w-1", 50)
	if err != nil || decisionIDs(owner) != "dec_2,dec_1" {
		t.Fatalf("owner calls = %q, err=%v", decisionIDs(owner), err)
	}
	needsYou, err := store.ListDecisionCallsByState(ctx, domain.DecisionStateNeedsYou, 200)
	if err != nil || decisionIDs(needsYou) != "dec_2,dec_3" {
		t.Fatalf("needs-you calls = %q, err=%v", decisionIDs(needsYou), err)
	}
	pending, err := store.ListDecisionCallsAwaitingDelivery(ctx, "h_a")
	if err != nil || decisionIDs(pending) != "dec_1" || pending[0].StandingEntryID != "dent_5" {
		t.Fatalf("pending calls = %q, err=%v", decisionIDs(pending), err)
	}
	appendDecision(t, store, decisionDelivered("dent_7", "h_a", "dec_1", "dent_5", "orchestrator", at.Add(6*time.Second)), at.Add(6*time.Second))
	fallback, err := store.ListDecisionCallsForSession(ctx, "h_a", "orchestrator", 50)
	if err != nil || decisionIDs(fallback) != "dec_1" {
		t.Fatalf("fallback-delivered calls = %q, err=%v", decisionIDs(fallback), err)
	}
	streams, err := store.ListDecisionStreams(ctx)
	wantStreams := []domain.DecisionStream{{Origin: "h_a", SessionID: "epic", Name: "Checkout epic"}}
	if err != nil || !reflect.DeepEqual(streams, wantStreams) {
		t.Fatalf("streams = %+v, err=%v; want %+v", streams, err, wantStreams)
	}
}

func TestDecisionDeliveryListKeepsEveryPendingStandingEntry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.UnixMilli(1_000_000).UTC()
	appendDecision(t, store, decisionRaised("raised", "h_a", "dec_1", "w-1", "", "", at), at)
	appendDecision(t, store, decisionAnswered("answer-1", "h_a", "dec_1", 1, at.Add(time.Second)), at.Add(time.Second))
	appendDecision(t, store, decisionAnswered("answer-2", "h_a", "dec_1", 2, at.Add(2*time.Second)), at.Add(2*time.Second))

	pending, err := store.ListDecisionCallsAwaitingDelivery(ctx, "h_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].StandingEntryID != "answer-1" || pending[0].Option != 1 ||
		pending[1].StandingEntryID != "answer-2" || pending[1].Option != 2 {
		t.Fatalf("pending = %+v", pending)
	}

	appendDecision(t, store, decisionDelivered("delivered-1", "h_a", "dec_1", "answer-1", "w-1", at.Add(3*time.Second)), at.Add(3*time.Second))
	pending, err = store.ListDecisionCallsAwaitingDelivery(ctx, "h_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].StandingEntryID != "answer-2" || !pending[0].Redelivery {
		t.Fatalf("pending after delivery = %+v", pending)
	}
}

func TestDecisionStreamsUseLedgerOrderWhenTheClockMovesBackward(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.UnixMilli(1_000_000).UTC()
	first, _ := appendDecision(t, store, decisionRaised("dent_1", "h_a", "dec_1", "w-1", "epic", "Checkout", at), at)
	second, _ := appendDecision(t, store, decisionRaised("dent_2", "h_a", "dec_2", "w-1", "epic", "Checkout epic", at.Add(-time.Hour)), at.Add(-time.Hour))
	if domain.CompareDecisionEntries(first, second) >= 0 {
		t.Fatalf("ledger order first=%+v second=%+v, want second later", first.Stamp, second.Stamp)
	}

	streams, err := store.ListDecisionStreams(ctx)
	want := []domain.DecisionStream{{Origin: "h_a", SessionID: "epic", Name: "Checkout epic"}}
	if err != nil || !reflect.DeepEqual(streams, want) {
		t.Fatalf("streams=%+v err=%v, want %+v", streams, err, want)
	}
}

func decisionIDs(calls []domain.DecisionCall) string {
	ids := make([]string, len(calls))
	for i, call := range calls {
		ids[i] = call.ID
	}
	return strings.Join(ids, ",")
}

func decisionEntryOf(entryID, origin, callID string, kind domain.DecisionEntryKind, payload any, writtenAt time.Time) domain.DecisionEntry {
	author := domain.DecisionAuthorPerson
	if kind == domain.DecisionKindOutcome || kind == domain.DecisionKindWithdrawn {
		author = domain.DecisionAuthorSession
	}
	return domain.DecisionEntry{
		ID: entryID, Origin: origin, CallID: callID, Kind: kind,
		Payload: mustDecisionJSON(payload), WrittenAt: writtenAt,
		AuthorKind: author, AuthorSurface: domain.RequestSurfaceCLI,
	}
}

// The board window holds every active call plus non-retired Done calls whose
// outcome landed in the last seven days. Retiring or re-reducing a call never
// moves its outcome timestamp, so neither can extend that window.
func TestDecisionBoardWindow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	since := now.Add(-7 * 24 * time.Hour)
	raisedAt := since.Add(-24 * time.Hour)

	done := func(callID string, outcomeAt time.Time) {
		appendDecision(t, store, decisionRaised(callID+"_r", "h_a", callID, "w-1", "", "", raisedAt), raisedAt)
		answer, _ := appendDecision(t, store, decisionAnswered(callID+"_a", "h_a", callID, 1, raisedAt.Add(time.Minute)), raisedAt.Add(time.Minute))
		appendDecision(t, store, decisionEntryOf(callID+"_o", "h_a", callID, domain.DecisionKindOutcome,
			domain.DecisionOutcomePayload{ForEntry: answer.ID, Text: "Shipped"}, outcomeAt), outcomeAt)
	}
	appendDecision(t, store, decisionRaised("needs_r", "h_a", "dec_needs", "w-1", "", "", raisedAt), raisedAt)
	appendDecision(t, store, decisionRaised("prog_r", "h_b", "dec_progress", "w-2", "", "", raisedAt.Add(time.Second)), raisedAt.Add(time.Second))
	appendDecision(t, store, decisionAnswered("prog_a", "h_b", "dec_progress", 1, raisedAt.Add(2*time.Second)), raisedAt.Add(2*time.Second))
	done("dec_edge", since)
	done("dec_old", since.Add(-time.Nanosecond))
	done("dec_recent", now.Add(-time.Hour))
	done("dec_retired", now.Add(-time.Hour))
	appendDecision(t, store, decisionEntryOf("retired_x", "h_a", "dec_retired", domain.DecisionKindRetired,
		domain.DecisionRetiredPayload{Retired: true}, now), now)
	appendDecision(t, store, decisionRaised("dis_r", "h_a", "dec_dismissed", "w-1", "", "", raisedAt), raisedAt)
	appendDecision(t, store, decisionEntryOf("dis_d", "h_a", "dec_dismissed", domain.DecisionKindDismissed,
		domain.DecisionDismissedPayload{Note: "not needed"}, now), now)
	appendDecision(t, store, decisionRaised("wd_r", "h_a", "dec_withdrawn", "w-1", "", "", raisedAt), raisedAt)
	appendDecision(t, store, decisionEntryOf("wd_w", "h_a", "dec_withdrawn", domain.DecisionKindWithdrawn, struct{}{}, now), now)

	board, err := store.ListDecisionCallsForBoard(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]domain.DecisionCall{}
	for _, call := range board {
		ids[call.ID] = call
	}
	for _, want := range []string{"dec_needs", "dec_progress", "dec_edge", "dec_recent"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("board %q is missing %s", decisionIDs(board), want)
		}
	}
	for _, absent := range []string{"dec_old", "dec_retired", "dec_dismissed", "dec_withdrawn"} {
		if _, ok := ids[absent]; ok {
			t.Fatalf("board %q includes %s", decisionIDs(board), absent)
		}
	}
	if len(board) != 4 {
		t.Fatalf("board = %q, want exactly four calls", decisionIDs(board))
	}
	stored, ok, err := store.GetDecisionCall(ctx, "dec_recent")
	if err != nil || !ok || !reflect.DeepEqual(ids["dec_recent"], stored) {
		t.Fatalf("board call differs from decoded call: %+v vs %+v (%v)", ids["dec_recent"], stored, err)
	}

	// Retiring then restoring a recent Done call, and a late delivery entry,
	// keep the original outcome timestamp.
	appendDecision(t, store, decisionEntryOf("recent_ret", "h_a", "dec_recent", domain.DecisionKindRetired,
		domain.DecisionRetiredPayload{Retired: true}, now.Add(time.Minute)), now.Add(time.Minute))
	appendDecision(t, store, decisionEntryOf("recent_unret", "h_a", "dec_recent", domain.DecisionKindRetired,
		domain.DecisionRetiredPayload{Retired: false}, now.Add(2*time.Minute)), now.Add(2*time.Minute))
	board, err = store.ListDecisionCallsForBoard(ctx, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := decisionIDs(board); got != "dec_needs,dec_progress" {
		t.Fatalf("board after retire/unretire with a 30-minute window = %q, want only active calls", got)
	}
}

func TestDecisionStreamsForSessionsReadOnlyThoseStreams(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.UnixMilli(1_000_000).UTC()
	appendDecision(t, store, decisionRaised("dent_1", "h_a", "dec_1", "w-1", "epic", "Checkout", at), at)
	appendDecision(t, store, decisionRaised("dent_2", "h_a", "dec_2", "w-1", "epic", "Checkout epic", at.Add(time.Second)), at.Add(time.Second))
	appendDecision(t, store, decisionRaised("dent_3", "h_b", "dec_3", "w-9", "epic", "Laptop epic", at.Add(2*time.Second)), at.Add(2*time.Second))
	appendDecision(t, store, decisionRaised("dent_4", "h_a", "dec_4", "w-2", "other", "Other", at.Add(3*time.Second)), at.Add(3*time.Second))

	streams, err := store.ListDecisionStreamsFor(ctx, []domain.SessionID{"epic", "missing"})
	want := []domain.DecisionStream{
		{Origin: "h_a", SessionID: "epic", Name: "Checkout epic"},
		{Origin: "h_b", SessionID: "epic", Name: "Laptop epic"},
	}
	if err != nil || !reflect.DeepEqual(streams, want) {
		t.Fatalf("streams = %+v, err=%v; want %+v", streams, err, want)
	}
	if streams, err := store.ListDecisionStreamsFor(ctx, nil); err != nil || len(streams) != 0 {
		t.Fatalf("no sessions = %+v, %v", streams, err)
	}
}
