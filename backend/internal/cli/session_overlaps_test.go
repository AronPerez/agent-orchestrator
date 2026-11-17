package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func TestSessionOverlapsHumanOutput(t *testing.T) {
	deps := overlapTestDeps(t, http.StatusOK, `{
		"projectId":"demo",
		"overlaps":[{"sessionA":"demo-1","sessionB":"demo-2","paths":["backend/main.go","frontend/src/App.tsx"]}],
		"truncatedSessionIds":["demo-3"]
	}`)

	out, errOut, err := executeCLI(t, deps, "session", "overlaps", "--project", "demo")
	if err != nil {
		t.Fatalf("session overlaps failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"SESSION A", "SESSION B", "SHARED PATHS", "demo-1", "demo-2", "backend/main.go, frontend/src/App.tsx", "demo-3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSessionOverlapsJSONOutput(t *testing.T) {
	deps := overlapTestDeps(t, http.StatusOK, `{"projectId":"demo","overlaps":[],"truncatedSessionIds":[]}`)

	out, errOut, err := executeCLI(t, deps, "session", "overlaps", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session overlaps --json failed: %v\nstderr=%s", err, errOut)
	}
	var got projectOverlapsResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, out)
	}
	if got.ProjectID != "demo" || got.Overlaps == nil || got.TruncatedSessionIDs == nil {
		t.Fatalf("response = %#v", got)
	}
}

func TestSessionOverlapsSurfacesDaemonError(t *testing.T) {
	deps := overlapTestDeps(t, http.StatusNotFound, `{"error":"not_found","code":"PROJECT_NOT_FOUND","message":"Unknown project","requestId":"req-1"}`)

	_, _, err := executeCLI(t, deps, "session", "overlaps", "--project", "missing")
	if err == nil {
		t.Fatal("expected daemon error")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(%v) = %d, want 1", err, got)
	}
	for _, want := range []string{"PROJECT_NOT_FOUND", "req-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestSessionOverlapsRejectsArguments(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "session", "overlaps", "extra")
	if err == nil {
		t.Fatal("expected usage error")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
	}
}

func overlapTestDeps(t *testing.T, overlapStatus int, overlapBody string) Deps {
	t.Helper()
	cfg := setConfigEnv(t)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}
	return Deps{
		ProcessAlive: func(int) bool { return true },
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.Method + " " + req.URL.Path {
			case "GET /api/v1/projects/demo", "GET /api/v1/projects/missing":
				return jsonResponse(http.StatusOK, `{"status":"ok","project":{"id":"`+strings.TrimPrefix(req.URL.Path, "/api/v1/projects/")+`","name":"Demo","path":"/repo/demo","defaultBranch":"develop"}}`), nil
			case "GET /api/v1/projects/demo/overlaps", "GET /api/v1/projects/missing/overlaps":
				return jsonResponse(overlapStatus, overlapBody), nil
			default:
				return jsonResponse(http.StatusNotFound, `{"error":{"kind":"not_found","code":"NOT_FOUND","message":"unexpected request"}}`), nil
			}
		})},
	}
}
