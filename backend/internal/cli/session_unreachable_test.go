package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// unreachableSessionsServer answers both listings with one worker and one
// orchestrator whose runtimes AO cannot reach.
func unreachableSessionsServer(t *testing.T) *httptest.Server {
	t.Helper()
	body := `{"sessions":[` +
		`{"id":"demo-1","projectId":"demo","kind":"worker","activity":{"state":"idle","lastActivityAt":"2026-06-02T12:00:00Z"},"isTerminated":false,"status":"idle","runtimeUnreachable":true},` +
		`{"id":"demo-2","projectId":"demo","kind":"orchestrator","activity":{"state":"idle","lastActivityAt":"2026-06-02T12:00:00Z"},"isTerminated":false,"status":"idle","runtimeUnreachable":true}` +
		`]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/orchestrators", r.URL.Query().Get("active") != "false":
			_, _ = io.WriteString(w, body)
		default:
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Both listings must stop printing a confident status for a session AO can no
// longer see; agreement between them is the point — a board that says [idle]
// one second before `ao send` fails is worse than no status at all.
func TestListingsReplaceStatusWhenRuntimeIsUnreachable(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"session ls", []string{"session", "ls", "--all"}},
		{"orchestrator ls", []string{"orchestrator", "ls"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := unreachableSessionsServer(t)
			writeRunFileFor(t, cfg, srv)

			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tc.args...)
			if err != nil {
				t.Fatalf("%v failed: %v\nstderr=%s", tc.args, err, errOut)
			}
			if !strings.Contains(out, "[unreachable]") {
				t.Fatalf("listing did not name the unreachable runtime:\n%s", out)
			}
			if strings.Contains(out, "[idle]") {
				t.Fatalf("listing still claims idle for an unreachable runtime:\n%s", out)
			}
		})
	}
}

// A reachable session keeps its derived status untouched.
func TestListingKeepsDerivedStatusWhenRuntimeIsReachable(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "ls")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "[working]") {
		t.Fatalf("reachable session lost its status:\n%s", out)
	}
	if strings.Contains(out, "unreachable") {
		t.Fatalf("reachable session was flagged unreachable:\n%s", out)
	}
}
