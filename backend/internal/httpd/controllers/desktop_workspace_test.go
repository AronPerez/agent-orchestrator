package controllers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeDesktopWorkspaceService struct {
	path string
	err  error
}

func (f fakeDesktopWorkspaceService) WorkspaceLocation(context.Context, domain.SessionID) (string, error) {
	return f.path, f.err
}

func desktopWorkspaceServer(t *testing.T, svc fakeDesktopWorkspaceService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		DesktopWorkspaces: svc,
	}, httpd.ControlDeps{}))
	t.Cleanup(server.Close)
	return server
}

func TestDesktopWorkspaceLocationReturnsPathToLoopbackSupervisor(t *testing.T) {
	server := desktopWorkspaceServer(t, fakeDesktopWorkspaceService{path: "/tmp/ao/worktrees/ao-1"})
	body, status, headers := doRequest(t, server, http.MethodGet, "/api/v1/desktop/sessions/ao-1/workspace", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET desktop workspace = %d, want 200; body=%s", status, body)
	}
	if !containsAll(body, `"sessionId":"ao-1"`, `"workspacePath":"/tmp/ao/worktrees/ao-1"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestDesktopWorkspaceLocationPreservesServiceNotFound(t *testing.T) {
	server := desktopWorkspaceServer(t, fakeDesktopWorkspaceService{
		err: apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace is not available"),
	})
	body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/desktop/sessions/ao-1/workspace", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestDesktopWorkspaceLocationSurfacesServiceFailure(t *testing.T) {
	server := desktopWorkspaceServer(t, fakeDesktopWorkspaceService{err: errors.New("storage unavailable")})
	body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/desktop/sessions/ao-1/workspace", "")
	assertErrorCode(t, body, status, http.StatusInternalServerError, "INTERNAL_ERROR")
}

// The desktop app resolves a REMOTE session's workspace through this gated
// twin: /api/v1/desktop is deliberately LAN-blocked, so the desktop route can
// never answer over the network, while this one is served with auth.
func TestSessionWorkspaceLocationGatedTwin(t *testing.T) {
	server := desktopWorkspaceServer(t, fakeDesktopWorkspaceService{path: "/tmp/ao/worktrees/ao-1"})

	body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/sessions/ao-1/workspace-location", "")
	if status != http.StatusOK {
		t.Fatalf("gated workspace-location = %d, want 200; body=%s", status, body)
	}
	var got struct {
		SessionID     string `json:"sessionId"`
		WorkspacePath string `json:"workspacePath"`
	}
	mustJSON(t, body, &got)
	if got.SessionID != "ao-1" || got.WorkspacePath == "" {
		t.Fatalf("body = %+v, want sessionId ao-1 and a workspacePath", got)
	}

	missing := desktopWorkspaceServer(t, fakeDesktopWorkspaceService{
		err: apierr.NotFound("SESSION_NOT_FOUND", "Session was not found"),
	})
	body, status, _ = doRequest(t, missing, http.MethodGet, "/api/v1/sessions/does-not-exist/workspace-location", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}
