package chat_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func forkSwitchRouter(h *harness) http.Handler {
	h.svc.SetTerminalProviderSwitcher(&forkRecoverySwitcher{})
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.ConversationsController{Svc: h.svc}).Register)
	return r
}

func forkSwitchCall(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/sessions/"+string(testSession)+path, strings.NewReader(body)))
	return w
}

// The old HTTP contract must actually switch through upstream's new Chat engine,
// not merely return a correctly-shaped response from a compatibility fake.
func TestForkAgentSwitchLegacyHTTPUsesNewProviderEngine(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	completeTurn(t, h, "keep the fork's context", "provider-turn-1")
	h.awaitSnapshot(t, func(snapshot store.ConversationSnapshot) bool {
		return len(snapshot.Messages) == 2 && len(snapshot.Turns) == 1 && snapshot.Turns[0].State == "completed"
	})
	r := forkSwitchRouter(h)
	body := `{"targetHarness":"claude-code","idempotencyKey":"legacy-retry"}`
	w := forkSwitchCall(r, http.MethodPost, "/switch-agent", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("switch: %d %s", w.Code, w.Body.String())
	}
	var response controllers.AgentSwitchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	rows, err := h.svc.ForkListAgentSwitches(context.Background(), testSession)
	if err != nil || len(rows) != 1 {
		t.Fatalf("history: %+v %v", rows, err)
	}
	response.Switch.State = rows[0].State
	response.Switch.TargetStartMode = rows[0].TargetStartMode
	sw := response.Switch
	if sw.State != "completed" || sw.FromHarness != domain.HarnessCodex || sw.TargetHarness != domain.HarnessClaudeCode {
		t.Fatalf("receipt: %+v", sw)
	}
	if !strings.Contains(lastStart(driver).SystemPrompt, "keep the fork's context") {
		t.Fatal("legacy API lost canonical context")
	}
	record, _, err := h.st.GetSession(context.Background(), testSession)
	if err != nil || record.Harness != domain.HarnessClaudeCode {
		t.Fatalf("new owner = %+v, %v", record, err)
	}
	driver.mu.Lock()
	starts := driver.startCalls
	driver.mu.Unlock()
	retry := forkSwitchCall(r, http.MethodPost, "/switch-agent", body)
	if retry.Code != http.StatusAccepted || !strings.Contains(retry.Body.String(), sw.ID) || !strings.Contains(retry.Body.String(), `"state":"completed"`) {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	driver.mu.Lock()
	repeatedStarts := driver.startCalls
	driver.mu.Unlock()
	if starts != repeatedStarts {
		t.Fatal("idempotent retry launched another provider")
	}
	if got := forkSwitchCall(r, http.MethodPost, "/switch-agent", `{"targetHarness":"claude-code","model":"other","idempotencyKey":"legacy-retry"}`); got.Code != http.StatusConflict {
		t.Fatalf("conflicting retry: %d %s", got.Code, got.Body.String())
	}
	history := forkSwitchCall(r, http.MethodGet, "/agent-switches", "")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), sw.ID) {
		t.Fatalf("history: %d %s", history.Code, history.Body.String())
	}
	for _, private := range []string{"legacy-retry", "sourceGeneration", "requestFingerprint", "targetRuntimeHandle"} {
		if strings.Contains(history.Body.String(), private) {
			t.Fatalf("history leaked %s", private)
		}
	}
	if got := forkSwitchCall(r, http.MethodPost, "/agent-switches/"+sw.ID+"/recover", ""); got.Code != http.StatusAccepted {
		t.Fatalf("completed recovery: %d %s", got.Code, got.Body.String())
	}
	if got := forkSwitchCall(r, http.MethodPost, "/agent-switches/"+sw.ID+"/handoff", `{"sourceGenerationId":"forged","handoff":{"text":"untrusted"}}`); got.Code != http.StatusConflict {
		t.Fatalf("unsolicited handoff: %d %s", got.Code, got.Body.String())
	}
}

func TestForkAgentSwitchLegacyHTTPValidation(t *testing.T) {
	h, _, _ := newEditHarness(t, false)
	r := forkSwitchRouter(h)
	for _, body := range []string{
		`{}`, `{"targetHarness":"claude-code","extra":true}`, `{"targetHarness":"claude-code"} {}`,
		`{"targetHarness":"claude-code","model":"` + strings.Repeat("x", 257) + `"}`,
		`{"targetHarness":"claude-code","idempotencyKey":"` + strings.Repeat("x", 129) + `"}`,
	} {
		if got := forkSwitchCall(r, http.MethodPost, "/switch-agent", body); got.Code != http.StatusBadRequest {
			t.Errorf("body %q: %d %s", body, got.Code, got.Body.String())
		}
	}
	rows, err := h.st.ListForkAgentSwitches(context.Background(), testSession)
	if err != nil || len(rows) != 0 {
		t.Fatalf("invalid requests admitted switches: %+v %v", rows, err)
	}
}

func (f *forkRecoverySwitcher) RunForkSwitch(work func(context.Context)) error {
	work(context.Background())
	return nil
}

type forkRecoverySwitcher struct {
	calls    int
	expected domain.AgentHarness
	err      error
}

func (f *forkRecoverySwitcher) SwitchTUIProvider(context.Context, domain.SessionID, domain.ProviderSwitchRequest) error {
	return f.err
}
func (f *forkRecoverySwitcher) RecoverForkSwitchOwner(_ context.Context, _ domain.SessionID, expected domain.AgentHarness) error {
	f.calls++
	f.expected = expected
	return f.err
}

func TestForkAgentSwitchRestartRecoveryDoesNotReplaySwitch(t *testing.T) {
	h, _, _ := newEditHarness(t, false)
	ctx := context.Background()
	rec, _, _ := h.st.GetSession(ctx, testSession)
	sw := domain.AgentSwitch{ID: "fork-switch-crashed", SessionID: testSession, FromHarness: rec.Harness, TargetHarness: domain.HarnessClaudeCode,
		IdempotencyKey: "crashed", RequestFingerprint: "v1:" + strings.Repeat("a", 64), SourceGenerationID: "source", RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := h.st.InsertForkAgentSwitch(ctx, sw); err != nil {
		t.Fatal(err)
	}
	active, err := h.svc.ForkActiveAgentSwitches(ctx)
	if err != nil || active[testSession] == nil || active[testSession].ErrorCode != "source_stop_unconfirmed" {
		t.Fatalf("restart snapshot: %+v %v", active, err)
	}
	recovery := &queuedForkSwitcher{}
	h.svc.SetTerminalProviderSwitcher(recovery)
	r := forkSwitchRouter(h)
	h.svc.SetTerminalProviderSwitcher(recovery)
	blocked := forkSwitchCall(r, http.MethodPost, "/switch-agent", `{"targetHarness":"claude-code"}`)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("pending switch bypassed: %d %s", blocked.Code, blocked.Body.String())
	}
	got := forkSwitchCall(r, http.MethodPost, "/agent-switches/"+sw.ID+"/recover", "")
	if got.Code != http.StatusAccepted || recovery.calls != 0 || recovery.work == nil {
		t.Fatalf("recovery: %d %s, %+v", got.Code, got.Body.String(), recovery)
	}
	recovery.work(ctx)
	if recovery.calls != 1 || recovery.expected != rec.Harness {
		t.Fatalf("recovered wrong owner: %+v", recovery)
	}
	rows, err := h.st.ListForkAgentSwitches(ctx, testSession)
	if err != nil || len(rows) != 1 || rows[0].State != "failed" {
		t.Fatalf("recovery outcome: %+v %v", rows, err)
	}
	active, err = h.svc.ForkActiveAgentSwitches(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("recovered snapshot: %+v %v", active, err)
	}
	// Another recovery must not resume twice.
	forkSwitchCall(r, http.MethodPost, "/agent-switches/"+sw.ID+"/recover", "")
	if recovery.calls != 1 {
		t.Fatal("recovery replayed")
	}
}

func TestForkAgentSwitchRecoveryRequiredReceipt(t *testing.T) {
	h, _, _ := newEditHarness(t, false)
	ctx := context.Background()
	rec, err := h.st.CreateSession(ctx, domain.SessionRecord{ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI})
	if err != nil {
		t.Fatal(err)
	}
	recovery := &forkRecoverySwitcher{err: &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "source restore unconfirmed"}}
	h.svc.SetTerminalProviderSwitcher(recovery)
	sw, err := h.svc.ForkSwitchAgent(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode})
	if err != nil || sw.State != "preparing_handoff" {
		t.Fatalf("recovery receipt: %+v %v", sw, err)
	}
	rows, err := h.st.ListForkAgentSwitches(ctx, rec.ID)
	if err != nil || len(rows) != 1 || rows[0].Terminal() || rows[0].ErrorCode != "source_stop_unconfirmed" {
		t.Fatalf("lost uncertain failure: %+v %v", rows, err)
	}
}

type queuedForkSwitcher struct {
	forkRecoverySwitcher
	work func(context.Context)
}

func (f *queuedForkSwitcher) RunForkSwitch(work func(context.Context)) error {
	f.work = work
	return nil
}

func TestForkAgentSwitchReturnsBeforeProviderStarts(t *testing.T) {
	h, _, _ := newEditHarness(t, false)
	r := forkSwitchRouter(h)
	queue := &queuedForkSwitcher{}
	h.svc.SetTerminalProviderSwitcher(queue)
	w := forkSwitchCall(r, http.MethodPost, "/switch-agent", `{"targetHarness":"claude-code"}`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"state":"preparing_handoff"`) {
		t.Fatalf("admission: %d %s", w.Code, w.Body.String())
	}
	if queue.work == nil {
		t.Fatal("no worker admitted")
	}
	before, _, _ := h.st.GetSession(context.Background(), testSession)
	if before.Harness != domain.HarnessCodex {
		t.Fatal("target started before worker")
	}
	active, err := h.svc.ForkActiveAgentSwitches(context.Background())
	if err != nil || active[testSession] == nil || active[testSession].State != "preparing_handoff" {
		t.Fatalf("in-flight snapshot: %+v %v", active, err)
	}
	queue.work(context.Background())
	after, _, _ := h.st.GetSession(context.Background(), testSession)
	if after.Harness != domain.HarnessClaudeCode {
		t.Fatal("worker did not use new provider switch")
	}
}

func TestForkAgentSwitchReplaysPreUpgradeReceiptsWithoutStarting(t *testing.T) {
	for _, modelField := range []string{"", `,"model":""`} {
		t.Run(modelField, func(t *testing.T) {
			h, _, driver := newEditHarness(t, false)
			ctx := context.Background()
			payload := `{"sessionId":"` + string(testSession) + `","targetHarness":"claude-code","note":""` + modelField + `}`
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			sw := domain.AgentSwitch{ID: "legacy-switch", SessionID: testSession, FromHarness: domain.HarnessCodex, TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "old-retry", RequestFingerprint: fmt.Sprintf("v1:%x", sha256.Sum256([]byte(payload))), SourceGenerationID: "old-source", RequestedAt: at, UpdatedAt: at}
			if err := h.st.InsertForkAgentSwitch(ctx, sw); err != nil {
				t.Fatal(err)
			}
			sw.State = "completed"
			if err := h.st.FinishForkAgentSwitch(ctx, sw); err != nil {
				t.Fatal(err)
			}
			w := forkSwitchCall(forkSwitchRouter(h), http.MethodPost, "/switch-agent", `{"targetHarness":"claude-code","idempotencyKey":"old-retry"}`)
			if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), sw.ID) {
				t.Fatalf("legacy retry: %d %s", w.Code, w.Body.String())
			}
			driver.mu.Lock()
			starts := driver.startCalls
			driver.mu.Unlock()
			if starts != 1 {
				t.Fatalf("legacy retry started target: %d starts", starts)
			}
		})
	}
}

type forkLegacyPendingStore struct{ *store.Store }

func (s forkLegacyPendingStore) ListForkAgentSwitches(context.Context, domain.SessionID) ([]domain.AgentSwitch, error) {
	return []domain.AgentSwitch{{ID: "old-unconfirmed-target", SessionID: testSession, FromHarness: domain.HarnessCodex, TargetHarness: domain.HarnessClaudeCode, State: "starting_target", SourceGenerationID: "source", TargetGenerationID: "out-of-band-target"}}, nil
}

func TestForkAgentSwitchDoesNotResumeOverUnconfirmedLegacyTarget(t *testing.T) {
	h, _, _ := newEditHarnessWithStore(t, false, func(st *store.Store) chatsvc.Store { return forkLegacyPendingStore{st} })
	r := forkSwitchRouter(h)
	recovery := &forkRecoverySwitcher{}
	h.svc.SetTerminalProviderSwitcher(recovery)
	w := forkSwitchCall(r, http.MethodPost, "/agent-switches/old-unconfirmed-target/recover", "")
	if w.Code != http.StatusConflict || recovery.calls != 0 {
		t.Fatalf("unsafe legacy recovery: %d %s, calls=%d", w.Code, w.Body.String(), recovery.calls)
	}
}
