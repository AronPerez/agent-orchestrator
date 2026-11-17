package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testSavedHostID    = "h_desk"
	testSavedHostLabel = "desk"
	testLocalHostID    = "h_local"
	testCoordination   = "aoc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func configureSavedHost(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	home := aoHome(t)
	_ = setConfigEnv(t)
	// An isolated desktop uses AO_DATA_DIR/remotes.json. These CLI boundary
	// tests use the ordinary ~/.ao/remotes.json layout and retain only the
	// separately isolated AO_RUN_FILE from setConfigEnv.
	t.Setenv("AO_DATA_DIR", "")
	identityDir := filepath.Join(home, ".ao", "data", "mobile")
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "identity.json"), []byte(`{"hostId":"`+testLocalHostID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: testSavedHostLabel, URL: srv.URL, HostID: testSavedHostID, CoordinationToken: testCoordination,
	})
	return srv
}

func requireSavedHostRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+testCoordination {
		t.Fatalf("%s %s Authorization = %q", r.Method, r.URL.Path, got)
	}
	if got := r.Header.Get("X-AO-Expected-Host-ID"); got != testSavedHostID {
		t.Fatalf("%s %s expected host = %q", r.Method, r.URL.Path, got)
	}
}

func writeSavedHostIdentity(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "" {
		t.Fatalf("identity probe sent Authorization %q", got)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"hostId":"h_desk","apiVersion":1}`)
}

func TestSpawnSavedHostPinsMachineAndOmitsUnqualifiedParent(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-parent")
	var request spawnRequest
	var paths []string
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/identity" {
			writeSavedHostIdentity(t, w, r)
			return
		}
		requireSavedHostRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","kind":"single-repo"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"remote-1","status":"idle","displayName":"Remote worker"}}`)
		default:
			http.NotFound(w, r)
		}
	}))

	out, stderr, err := executeCLI(t, Deps{}, "spawn", "--host", testSavedHostLabel,
		"--project", "demo", "--agent", "codex", "--name", "Remote worker")
	if err != nil {
		t.Fatalf("spawn --host: %v stderr=%s", err, stderr)
	}
	if request.ParentSessionID != "" {
		t.Fatalf("remote parentSessionId = %q, want empty until it is host-qualified", request.ParentSessionID)
	}
	if !strings.Contains(out, "spawned session remote-1") || !strings.Contains(out, "desk (h_desk)") {
		t.Fatalf("output = %q, want session and destination machine", out)
	}
	want := []string{
		"GET /api/v1/identity",
		"GET /api/v1/projects/demo",
		"POST /api/v1/agents/readiness/ensure",
		"POST /api/v1/sessions",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestSendSavedHostPinsAndNamesEveryWrite(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		path       string
		body       string
		payloadKey string
		message    string
	}{
		{name: "send", args: []string{"--message", "continue"}, path: "/api/v1/sessions/remote-1/send", body: `{"ok":true}`, payloadKey: "message", message: "continue"},
		{name: "steer", args: []string{"--steer", "--message", "correct course"}, path: "/api/v1/sessions/remote-1/conversation/steer-or-send", body: `{"outcome":"steered","providerTurnId":"turn-1"}`, payloadKey: "text", message: "correct course"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "local-source")
			var writes int
			configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/identity" {
					writeSavedHostIdentity(t, w, r)
					return
				}
				requireSavedHostRequest(t, r)
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					http.NotFound(w, r)
					return
				}
				writes++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if sender, ok := payload["senderSessionId"]; ok {
					t.Fatalf("remote write carried unqualified senderSessionId %v", sender)
				}
				if got, want := payload[tc.payloadKey], "[from local-source on "+testLocalHostID+"] "+tc.message; got != want {
					t.Fatalf("remote message source = %q, want %q", got, want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			args := append([]string{"send", "--host", testSavedHostLabel, "--session", "remote-1"}, tc.args...)
			out, stderr, err := executeCLI(t, Deps{}, args...)
			if err != nil {
				t.Fatalf("send --host: %v stderr=%s", err, stderr)
			}
			if writes != 1 || !strings.Contains(out, "desk (h_desk)") {
				t.Fatalf("writes=%d output=%q, want one machine-qualified write", writes, out)
			}
		})
	}
}

func TestSendSavedHostRecoveryKeepsTarget(t *testing.T) {
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			writeSavedHostIdentity(t, w, r)
			return
		}
		requireSavedHostRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"CHAT_STEER_UNCERTAIN","message":"unknown"}`)
	}))

	_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel, "--session", "remote-1",
		"--steer", "--client-message-id", "delivery-1", "--message", "correct course")
	if err == nil || !strings.Contains(err.Error(), "ao send --host 'h_desk'") {
		t.Fatalf("err = %v, want host-qualified recovery command", err)
	}
}

func TestSendSavedHostAgentSourceRequiresLocalHostIdentity(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-source")
	var authenticatedWrites int
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			writeSavedHostIdentity(t, w, r)
			return
		}
		authenticatedWrites++
		http.NotFound(w, r)
	}))
	if err := os.Remove(filepath.Join(os.Getenv("HOME"), ".ao", "data", "mobile", "identity.json")); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel, "--session", "remote-1", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "local host identity is unavailable") {
		t.Fatalf("err = %v, want missing origin-machine identity error", err)
	}
	if authenticatedWrites != 0 {
		t.Fatalf("authenticated writes = %d, want none", authenticatedWrites)
	}
}

func TestSavedHostRetryArgsQuotesOpaqueHostID(t *testing.T) {
	c := &commandContext{remote: &remoteTarget{hostID: "h_'$(unsafe)"}}
	if got, want := c.savedHostRetryArgs(), ` --host 'h_'"'"'$(unsafe)'`; got != want {
		t.Fatalf("savedHostRetryArgs() = %q, want %q", got, want)
	}
}

func TestSavedHostActionsRejectConflictingOrUnsupportedTargets(t *testing.T) {
	t.Run("send host conflicts with url", func(t *testing.T) {
		aoHome(t)
		t.Setenv("AO_TOKEN", "explicit-token")
		_, _, err := executeCLI(t, Deps{}, "send", "--url", "http://remote.example:3011", "--host", "desk",
			"--session", "remote-1", "--message", "continue")
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--host cannot be combined") {
			t.Fatalf("err=%v exit=%d, want target-conflict usage error", err, ExitCode(err))
		}
	})

	t.Run("spawn host implicit project never recommends raw url", func(t *testing.T) {
		t.Setenv("AO_PROJECT_ID", "local-project")
		configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/identity" {
				writeSavedHostIdentity(t, w, r)
				return
			}
			http.NotFound(w, r)
		}))
		_, _, err := executeCLI(t, Deps{}, "spawn", "--host", testSavedHostLabel, "--agent", "codex", "--name", "worker")
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "pass --project") {
			t.Fatalf("err=%v exit=%d, want explicit remote-project usage error", err, ExitCode(err))
		}
		if strings.Contains(err.Error(), "--url") {
			t.Fatalf("saved-host error recommended raw URL targeting: %v", err)
		}
	})

	t.Run("spawn claim is rejected before host lookup", func(t *testing.T) {
		aoHome(t)
		_, _, err := executeCLI(t, Deps{}, "spawn", "--host", "missing", "--project", "demo", "--agent", "codex",
			"--name", "worker", "--claim-pr", "42")
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--claim-pr is not supported with --host") {
			t.Fatalf("err=%v exit=%d, want remote-claim usage error", err, ExitCode(err))
		}
	})
}
