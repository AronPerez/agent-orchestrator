package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Chat's Plan toggle is stored with the conversation, so a daemon restart
// keeps it: work queued with Plan on still plans, and a live reconnect knows
// the choice the running turn was made under.
func TestConversationPlanModeSurvivesARestart(t *testing.T) {
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	ctx := context.Background()
	seedProject(t, s, "plan")
	rec, err := s.CreateSession(ctx, sampleRecord("plan"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	conversation, err := s.CreateConversation(ctx, "conv-plan", domain.ConversationScopeSession, rec.ProjectID, rec.ID, now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if conversation.Settings.PlanMode {
		t.Fatal("a new conversation must start with Plan off")
	}

	settings := domain.ConversationSettings{Model: "gemini-4-neon", ApprovalMode: domain.PermissionModeAuto, PlanMode: true}
	if err := s.SetConversationSettings(ctx, conversation.ID, settings, now); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	// CreateConversation returns the existing row, read back from storage.
	reread := func() domain.ConversationSettings {
		t.Helper()
		row, err := s.CreateConversation(ctx, "conv-plan", domain.ConversationScopeSession, rec.ProjectID, rec.ID, now)
		if err != nil {
			t.Fatalf("reread conversation: %v", err)
		}
		return row.Settings
	}
	if got := reread(); !got.PlanMode || got.Model != "gemini-4-neon" {
		t.Fatalf("stored settings = %+v, want Plan on with the chosen model", got)
	}

	settings.PlanMode = false
	if err := s.SetConversationSettings(ctx, conversation.ID, settings, now); err != nil {
		t.Fatalf("clear Plan: %v", err)
	}
	if reread().PlanMode {
		t.Fatal("turning Plan off must be stored too")
	}
}
