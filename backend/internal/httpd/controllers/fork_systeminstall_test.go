package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
)

func TestForkInstallJobRemainsActiveWhileWaiting(t *testing.T) {
	installer := &fakeInstaller{}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Installer: installer}, httpd.ControlDeps{}))
	defer srv.Close()
	for _, state := range []systeminstall.Status{
		systeminstall.StatusWaitingForIdle, systeminstall.StatusInstalling, systeminstall.StatusVerifying,
		systeminstall.StatusSucceeded, systeminstall.StatusFailed, systeminstall.StatusInterrupted,
	} {
		job := systeminstall.Job{Target: systeminstall.TargetCodex, Status: state}
		installer.startJob, installer.statusJob, installer.agentJobs = job, job, []systeminstall.Job{job}
		for _, route := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/agents/codex/install", `{"operation":"update"}`},
			{http.MethodGet, "/api/v1/agents/codex/install", ""},
			{http.MethodGet, "/api/v1/agents/install-jobs", ""},
		} {
			body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
			if status != http.StatusOK && status != http.StatusAccepted {
				t.Fatalf("%s %s: %d %s", route.method, route.path, status, body)
			}
			var view systeminstall.Job
			if route.path == "/api/v1/agents/install-jobs" {
				var list struct{ Jobs []systeminstall.Job }
				if err := json.Unmarshal(body, &list); err != nil || len(list.Jobs) != 1 {
					t.Fatalf("jobs = %s, %v", body, err)
				}
				view = list.Jobs[0]
			} else if err := json.Unmarshal(body, &view); err != nil {
				t.Fatal(err)
			}
			// Mirrors HarnessSettingsSection.isActive: both polling and action disabling use it.
			active := view.Status == systeminstall.StatusInstalling || view.Status == systeminstall.StatusVerifying
			wantActive := state == systeminstall.StatusWaitingForIdle || state == systeminstall.StatusInstalling || state == systeminstall.StatusVerifying
			if active != wantActive || !wantActive && view.Status != state {
				t.Fatalf("%s: internal=%s view=%s active=%v", route.path, state, view.Status, active)
			}
		}
		if installer.startJob.Status != state || installer.statusJob.Status != state || installer.agentJobs[0].Status != state {
			t.Fatal("HTTP projection mutated installer state")
		}
	}
}
