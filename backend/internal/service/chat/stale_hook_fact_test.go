package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// settledReplay is what an ACP session/load hands back for a thread whose last
// turn completed normally: one completed turn with a user prompt and an answer.
func settledReplay() []ports.ChatEvent {
	return []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "h-start", ProviderTurnID: "native-turn-1"},
		{
			Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "h-user",
			ProviderTurnID: "native-turn-1", ProviderItemID: "h-item-1",
			Text: "Review PR #15551.",
		},
		{
			Kind: ports.ChatEventMessageCompleted, ProviderEventID: "h-assistant",
			ProviderTurnID: "native-turn-1", ProviderItemID: "h-item-2",
			Text: "## Report\n\nHead reviewed: cf4ea2b922.",
		},
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "h-done", ProviderTurnID: "native-turn-1"},
	}
}

func startHandoff(t *testing.T, conv ports.ChatConversation, st *sqlite.Store) error {
	t.Helper()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns,
				Messages:     rows.Messages,
				Activities:   rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("stale-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	_, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", RequireNativeHistory: true,
	})
	return err
}

// A hook fact the provider can never reproduce must not strand the session.
//
// latest_assistant_update is a write-if-non-empty cache that nothing reconciles,
// so any hook dropped while the daemon was restarting leaves a value no replay
// contains. Measured on a live install: 96 of 120 TUI claude-code sessions held
// such a value. On ACP the replay is a frozen snapshot captured during Resume, so
// re-reading can never change the verdict and the switch failed permanently.
func TestInterfaceHandoffImportsDespiteUnreachableHookFact(t *testing.T) {
	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Review PR #15551."
	// Nowhere in the provider thread — the shape seen in the field.
	rec.Metadata.LatestAssistantUpdate = "next PR"
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events:           settledReplay(),
	}
	if err := startHandoff(t, conv, st); err != nil {
		t.Fatalf("Start = %v, want the handoff to import the settled replay anyway", err)
	}
	// The bypass must not turn into a poll: one read, then import.
	if reads := conv.historyReads(); reads != 1 {
		t.Fatalf("history reads = %d, want exactly one (no settle polling)", reads)
	}
}

// The bypass is deliberately narrow. AO's own high-water mark is written by the
// projector from provider events AO actually observed, so a replay that cannot
// reproduce it is genuinely missing history the user was already shown. That
// half stays fatal.
func TestInterfaceHandoffStillRejectsUnreachableAOHighWater(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	rec, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	// No hook facts at all, so only the high-water half can fail.
	rec.Metadata.LatestUserPrompt = ""
	rec.Metadata.LatestAssistantUpdate = ""
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	// A completed turn AO already projected, which the replay below never mentions.
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	existing, err := st.CreateConversation(
		ctx, "highwater-conversation", domain.ConversationScopeSession, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, testSession, "old-generation", now); err != nil {
		t.Fatalf("ClaimChatControllerGeneration: %v", err)
	}
	created, err := st.AppendUserMessage(ctx, existing.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID: "hw-user", Text: "Run the last verification.", Origin: domain.MessageOriginHuman,
			ClientMessageID: "hw-client-1",
		}, "hw-turn", now)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(ctx, "hw-turn", "native-turn-9", now); err != nil {
		t.Fatalf("BindTurnToProvider: %v", err)
	}
	if err := st.SettleAssistantMessage(ctx, existing.ID,
		"native-answer-9", "native-turn-9", "Verification passed.", "hw-answer", now); err != nil {
		t.Fatalf("SettleAssistantMessage: %v", err)
	}
	if err := st.SettleTurn(
		ctx, existing.ID, "native-turn-9", domain.TurnStateCompleted, "", now); err != nil {
		t.Fatalf("SettleTurn: %v", err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events:           settledReplay(), // only native-turn-1; native-turn-9 is absent
	}
	err = startHandoff(t, conv, st)
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start = %v, want ErrChatHistoryUnsettled for an unreachable AO high-water mark", err)
	}
}

// refreshableStaleConversation is a driver that CAN be re-read (Codex-shaped) but
// whose thread never converges on the hook facts, so the settle window expires.
// It exercises the deadline arm of the last-resort bypass, which is a different
// branch from the immutable-snapshot arm above.
type refreshableStaleConversation struct {
	*fakeConversation
	mu        sync.Mutex
	reads     int
	refreshes int
	events    []ports.ChatEvent
}

func (c *refreshableStaleConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return c.events, nil
}

// RefreshHistory performs a real second observation and keeps returning the same
// stale thread, so the settle window expires with the hook fact still unreachable.
func (c *refreshableStaleConversation) RefreshHistory(context.Context) ([]ports.ChatEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshes++
	return c.events, nil
}

func TestInterfaceHandoffImportsAfterSettleWindowExpiresOnStaleHookFact(t *testing.T) {
	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Review PR #15551."
	rec.Metadata.LatestAssistantUpdate = "next PR"
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	// Shrink the settle window rather than cancelling the caller's context: a
	// cancelled request must still fail, and does (see the upstream
	// ...ContextEndsBeforeRefresh test). This exercises the deadline instead.
	defer chatsvc.SetNativeHistorySettleLimitForTest(300 * time.Millisecond)()
	conv := &refreshableStaleConversation{
		fakeConversation: newFakeConversation(),
		events:           settledReplay(),
	}

	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("expire-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", RequireNativeHistory: true,
	}); err != nil {
		t.Fatalf("Start = %v, want the handoff to import once the settle window expired", err)
	}
	// A refreshable driver must still have been given its chance to converge
	// before the bypass applied: the bypass is a last resort, not a first choice.
	conv.mu.Lock()
	refreshes := conv.refreshes
	conv.mu.Unlock()
	if refreshes == 0 {
		t.Fatal("expected the refreshable driver to be polled before the bypass applied")
	}
}
