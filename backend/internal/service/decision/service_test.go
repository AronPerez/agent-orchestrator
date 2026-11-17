package decision

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type decisionServiceFixture struct {
	service  *Service
	store    *sqlite.Store
	appended *[]domain.DecisionEntry
	orch     domain.SessionID
	epic     domain.SessionID
	w1       domain.SessionID
	w2       domain.SessionID
	loose    domain.SessionID
}

func newDecisionService(t *testing.T) decisionServiceFixture {
	t.Helper()
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	base := time.UnixMilli(1_000_000).UTC()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "shop", Path: t.TempDir(), DisplayName: "Shop", RegisteredAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	create := func(project domain.ProjectID, kind domain.SessionKind, name string, parent domain.SessionID) domain.SessionID {
		t.Helper()
		record, err := store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: project, ParentSessionID: parent, Kind: kind, Harness: domain.HarnessFake,
			DisplayName: name, Mode: domain.SessionModeTUI,
			Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: base},
			Metadata:  domain.SessionMetadata{Branch: "ao/test/" + name, WorkspacePath: "/tmp/" + name},
			CreatedAt: base, UpdatedAt: base, AutoInjectReview: true, AutoInjectCI: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return record.ID
	}
	orch := create("shop", domain.KindOrchestrator, "Orchestrator", "")
	epic := create("shop", domain.KindWorker, "Checkout epic", orch)
	w1 := create("shop", domain.KindWorker, "w-1", epic)
	w2 := create("shop", domain.KindWorker, "w-2", epic)
	loose := create("", domain.KindWorker, "loose", "")

	now := base
	clock := func() time.Time {
		at := now
		now = now.Add(time.Second)
		return at
	}
	nextID := 0
	newID := func(prefix string) string {
		nextID++
		return fmt.Sprintf("%s%d", prefix, nextID)
	}
	var appended []domain.DecisionEntry
	service := New(Deps{
		Store: store, HostID: "h_a", MachineLabel: func() string { return "mac-mini" },
		Now: clock, NewID: newID, OnAppended: func(entry domain.DecisionEntry) { appended = append(appended, entry) },
	})
	return decisionServiceFixture{
		service: service, store: store, appended: &appended,
		orch: orch, epic: epic, w1: w1, w2: w2, loose: loose,
	}
}

func validRaiseInput() RaiseInput {
	return RaiseInput{
		Title:   "Which auth scheme should the proxy use?",
		Context: "The connector calls the proxy for each tenant.",
		Options: []domain.DecisionOption{
			{Label: "OAuth client credentials", Consequence: "One app registration per tenant.", Recommended: true},
			{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand."},
		},
	}
}

func TestRaiseRecordsWhoWhereAndWhichStream(t *testing.T) {
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	if call.ID != "dec_1" || call.Origin != "h_a" || call.State != domain.DecisionStateNeedsYou {
		t.Fatalf("call = %+v", call)
	}
	if call.Raised.ProjectID != "shop" || call.Raised.ProjectName != "Shop" || call.Raised.MachineLabel != "mac-mini" ||
		call.Raised.OwnerSessionID != fixture.w1 || call.Raised.OwnerSessionName != "w-1" ||
		call.Raised.StreamSessionID != fixture.epic || call.Raised.StreamName != "Checkout epic" {
		t.Fatalf("raised payload = %+v", call.Raised)
	}
	entries := *fixture.appended
	if len(entries) != 1 || entries[0].ID != "dent_2" || entries[0].Kind != domain.DecisionKindRaised ||
		entries[0].AuthorKind != domain.DecisionAuthorSession || entries[0].AuthorSession != fixture.w1 {
		t.Fatalf("appended = %+v", entries)
	}
}

func TestRaiseFromASessionWithNoProject(t *testing.T) {
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.loose}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	if call.Raised.ProjectID != "" || call.Raised.StreamSessionID != "" || call.Raised.OwnerSessionID != fixture.loose {
		t.Fatalf("raised payload = %+v", call.Raised)
	}
}

func TestRaiseRefusals(t *testing.T) {
	fixture := newDecisionService(t)
	if _, err := fixture.service.Raise(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, validRaiseInput()); apiCode(err) != "DECISION_SESSION_REQUIRED" {
		t.Fatalf("no session error = %v", err)
	}
	if _, err := fixture.service.Raise(context.Background(), Actor{Session: "ghost"}, validRaiseInput()); apiCode(err) != "SESSION_NOT_FOUND" {
		t.Fatalf("unknown session error = %v", err)
	}
	invalid := validRaiseInput()
	invalid.Options[0].Recommended = false
	if _, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, invalid); apiCode(err) != "DECISION_INVALID" {
		t.Fatalf("invalid call error = %v", err)
	} else {
		var apiError *apierr.Error
		if !errors.As(err, &apiError) || apiError.Details["rule"] != "recommended_count" || apiError.Message != "Mark exactly one option as recommended; this call marks 0." {
			t.Fatalf("invalid call details = %+v", apiError)
		}
	}
	withoutHost := New(Deps{Store: fixture.store, MachineLabel: func() string { return "mac-mini" }})
	if _, err := withoutHost.Raise(context.Background(), Actor{Session: fixture.w1}, validRaiseInput()); apiCode(err) != "DECISION_HOST_UNKNOWN" {
		t.Fatalf("host error = %v", err)
	}
}

func TestListAndGet(t *testing.T) {
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
	calls, err := fixture.service.List(ctx, Actor{Session: fixture.w1}, ScopeMine)
	if err != nil || callIDs(calls) != mine.ID {
		t.Fatalf("mine = %q, err=%v", callIDs(calls), err)
	}
	calls, err = fixture.service.List(ctx, Actor{Surface: domain.RequestSurfaceCLI}, ScopeNeedsYou)
	if err != nil || callIDs(calls) != mine.ID+","+theirs.ID+","+early.ID {
		t.Fatalf("needs you = %q, err=%v", callIDs(calls), err)
	}
	if _, err := fixture.service.List(ctx, Actor{Surface: domain.RequestSurfaceCLI}, ScopeMine); apiCode(err) != "DECISION_SESSION_REQUIRED" {
		t.Fatalf("mine without session error = %v", err)
	}
	if _, err := fixture.service.List(ctx, Actor{Session: fixture.w1}, Scope("everything")); apiCode(err) != "DECISION_SCOPE_INVALID" {
		t.Fatalf("invalid scope error = %v", err)
	}
	detail, err := fixture.service.Get(ctx, early.ID)
	if err != nil || len(detail.Entries) != 1 || detail.Call.Raised.StreamSessionID != fixture.epic || detail.Call.Raised.StreamName != "Checkout epic" {
		t.Fatalf("detail = %+v, err=%v", detail, err)
	}
	if _, err := fixture.service.Get(ctx, "dec_missing"); apiCode(err) != "DECISION_NOT_FOUND" {
		t.Fatalf("missing detail error = %v", err)
	}
}

func TestSearchPutsTheSameProjectFirst(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	elsewhere, err := fixture.service.Raise(ctx, Actor{Session: fixture.loose}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	here, err := fixture.service.Raise(ctx, Actor{Session: fixture.w2}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	calls, err := fixture.service.Search(ctx, Actor{Session: fixture.w1}, "auth scheme")
	if err != nil || callIDs(calls) != here.ID+","+elsewhere.ID {
		t.Fatalf("search = %q, err=%v", callIDs(calls), err)
	}
	if _, err := fixture.service.Search(ctx, Actor{Session: fixture.w1}, "  "); apiCode(err) != "DECISION_QUERY_REQUIRED" {
		t.Fatalf("blank search error = %v", err)
	}
}

func apiCode(err error) string {
	var apiError *apierr.Error
	if errors.As(err, &apiError) {
		return apiError.Code
	}
	return ""
}

func callIDs(calls []domain.DecisionCall) string {
	result := ""
	for i, call := range calls {
		if i > 0 {
			result += ","
		}
		result += call.ID
	}
	return result
}
