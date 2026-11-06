package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type spawnDefaultsCatalog struct{}

func (spawnDefaultsCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	return ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gpt-6.1-terra", Efforts: []string{"low", "high"}},
		{ID: "gpt-custom", Efforts: []string{"low", "high"}},
		{ID: "opus", Efforts: []string{"high", "max"}},
	}}, nil
}

func TestRemoteAPISpawnUsesRoleDefaultsAndExplicitOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		harness          domain.AgentHarness
		model, effort    string
	}{
		{"worker", "/api/v1/sessions", `{"projectId":"mer","kind":"worker","mode":"tui"}`, domain.HarnessCodex, "gpt-6.1-terra", "high"},
		{"orchestrator", "/api/v1/sessions", `{"projectId":"mer","kind":"orchestrator","mode":"tui"}`, domain.HarnessClaudeCode, "opus", "max"},
		{"delegate", "/api/v1/orchestrators/delegate", `{"projectId":"mer","mode":"tui"}`, domain.HarnessCodex, "gpt-6.1-terra", "high"},
		{"orchestrator endpoint", "/api/v1/orchestrators", `{"projectId":"mer","mode":"tui"}`, domain.HarnessClaudeCode, "opus", "max"},
		{"explicit worker harness", "/api/v1/sessions", `{"projectId":"mer","harness":"claude-code","mode":"tui"}`, domain.HarnessClaudeCode, "", ""},
		{"explicit orchestrator harness", "/api/v1/sessions", `{"projectId":"mer","kind":"orchestrator","harness":"codex","mode":"tui"}`, domain.HarnessCodex, "", ""},
		{"explicit delegate harness", "/api/v1/orchestrators/delegate", `{"projectId":"mer","agent":"claude-code","mode":"tui"}`, domain.HarnessClaudeCode, "", ""},
		{"explicit worker tuning", "/api/v1/sessions", `{"projectId":"mer","model":"gpt-custom","effort":"low","mode":"tui"}`, domain.HarnessCodex, "gpt-custom", "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t)
			s.mgr.SetModelCatalog(spawnDefaultsCatalog{})
			ctx := context.Background()
			if err := s.store.UpsertProject(ctx, domain.ProjectRecord{
				ID: "mer", Path: "/repo/mer", RegisteredAt: time.Now(),
				Config: domain.ProjectConfig{
					Worker:       domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "gpt-6.1-terra", Effort: "high"}},
					Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "opus", Effort: "max"}},
				},
			}); err != nil {
				t.Fatal(err)
			}
			router := httpd.NewRouterWithControl(config.Config{Agent: "claude-code"}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: s.sm}, httpd.ControlDeps{})
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
			req.RemoteAddr = "192.168.1.227:54321"
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code < 200 || w.Code >= 300 {
				t.Fatalf("POST %s: %d %s", tc.path, w.Code, w.Body.String())
			}
			var response struct {
				Session struct {
					ID domain.SessionID `json:"id"`
				} `json:"session"`
				Orchestrator struct {
					ID domain.SessionID `json:"id"`
				} `json:"orchestrator"`
				WorkerID domain.SessionID `json:"workerId"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			id := response.Session.ID
			if id == "" {
				id = response.WorkerID
			}
			if id == "" {
				id = response.Orchestrator.ID
			}
			rec, found, err := s.store.GetSession(ctx, id)
			if err != nil || !found {
				t.Fatalf("get spawned session %q: found=%v err=%v; response=%s", id, found, err, w.Body.String())
			}
			if rec.Harness != tc.harness || rec.Metadata.Model != tc.model || rec.Metadata.Effort != tc.effort {
				t.Fatalf("spawned harness/model/effort = %q/%q/%q, want %q/%q/%q", rec.Harness, rec.Metadata.Model, rec.Metadata.Effort, tc.harness, tc.model, tc.effort)
			}
		})
	}
}
