package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestSessionParentSurvivesRestartAndUpdates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "fleet")
	parent, err := s.CreateSession(ctx, sampleRecord("fleet"))
	if err != nil {
		t.Fatal(err)
	}
	child := sampleRecord("fleet")
	child.ParentSessionID = parent.ID
	child, err = s.CreateSession(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	child.ParentSessionID = "attempted-reparent"
	if err := s.UpdateSession(ctx, child); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(ctx, child.ID, "Renamed worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok, err := s.GetSession(ctx, child.ID)
	if err != nil || !ok || got.ParentSessionID != parent.ID {
		t.Fatalf("child=%+v ok=%v err=%v", got, ok, err)
	}
	rows, err := s.ListSessions(ctx, "fleet")
	if err != nil || len(rows) != 2 || rows[1].ParentSessionID != parent.ID || rows[0].ParentSessionID != "" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestSessionCoordinationBoundedAndAttributed(t *testing.T) {
	s, receiver, conversation := conversationFixture(t)
	ctx := context.Background()
	rec, _, err := s.GetSession(ctx, receiver)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := s.CreateSession(ctx, sampleRecord(string(rec.ProjectID)))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("msg-%02d", i)
		_, err := s.AppendUserMessage(ctx, conversation, receiver, "gen-1", domain.ConversationMessage{ID: id, Origin: domain.MessageOriginAutomation, SenderSessionID: string(sender.ID), Text: strings.Repeat("界", 260)}, "turn-"+id, histClock.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.AppendUserMessage(ctx, conversation, receiver, "gen-1", domain.ConversationMessage{ID: "human", Origin: domain.MessageOriginHuman, Text: "[from fake] not attribution"}, "turn-human", histClock.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateReport(ctx, domain.ReportRecord{ID: "report", SessionID: sender.ID, ProjectID: sender.ProjectID, State: domain.ReportState("checkpoint"), Note: "Verified focused tests", CreatedAt: histClock.Add(2 * time.Hour), AvailableAt: histClock.Add(2 * time.Hour), DeliveryState: domain.ReportPending, RepeatCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.SessionID{sender.ID, receiver} {
		events, err := s.ListSessionCoordination(ctx, id)
		if err != nil || len(events) != 51 {
			t.Fatalf("events=%d err=%v", len(events), err)
		}
		if id == sender.ID && (events[0].Kind != "report" || events[0].TargetSessionID != "" || events[0].State != "pending") {
			t.Fatalf("report=%+v", events[0])
		}
		for _, event := range events {
			if event.Kind == "message" && (event.SourceSessionID != string(sender.ID) || event.TargetSessionID != string(receiver) || len([]rune(event.Summary)) != 240 || event.State != "recorded") {
				t.Fatalf("message=%+v", event)
			}
		}
	}
	unrelated, err := s.CreateSession(ctx, sampleRecord(string(rec.ProjectID)))
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ListSessionCoordination(ctx, unrelated.ID)
	if err != nil || len(events) != 0 {
		t.Fatalf("unrelated=%+v err=%v", events, err)
	}
}
