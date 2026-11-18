package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
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
	cfg := setConfigEnv(t)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"`+testLocalHostID+`","apiVersion":1}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/sessions/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
			_, _ = io.WriteString(w, `{"session":{"id":`+strconv.Quote(id)+`}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(local.Close)
	writeRunFileFor(t, cfg, local)
	// The desktop-owned host store is independent of the protected data root
	// injected into this launched session.
	t.Setenv(remotesFileEnv, filepath.Join(home, ".ao", remotesFileName))
	identityDir := filepath.Join(cfg.dataDir, "mobile")
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

func writeSavedHostCapabilities(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	requireSavedHostRequest(t, r)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"capabilities":["automation-authored","host-qualified-parent"]}`)
}

func requireSavedHostRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+testCoordination {
		t.Fatalf("%s %s Authorization = %q", r.Method, r.URL.Path, got)
	}
	if got := r.Header.Get("X-AO-Expected-Host-ID"); got != testSavedHostID {
		t.Fatalf("%s %s expected host = %q", r.Method, r.URL.Path, got)
	}
	if got := r.Header.Get(caller.Header); got != "" {
		t.Fatalf("%s %s carried unqualified caller session %q", r.Method, r.URL.Path, got)
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

func TestSpawnSavedHostUsesSelectedSourceDaemonForQualifiedParent(t *testing.T) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/coordination/capabilities":
			writeSavedHostCapabilities(t, w, r)
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
	if request.ParentSessionID != "local-parent" || request.ParentHostID != testLocalHostID {
		t.Fatalf("remote parent = %q/%q, want %q/%q", request.ParentHostID, request.ParentSessionID, testLocalHostID, "local-parent")
	}
	if !strings.Contains(out, "spawned session remote-1") || !strings.Contains(out, "desk (h_desk)") {
		t.Fatalf("output = %q, want session and destination machine", out)
	}
	want := []string{
		"GET /api/v1/identity",
		"GET /api/v1/identity",
		"GET /api/v1/projects/demo",
		"GET /api/v1/identity",
		"POST /api/v1/agents/readiness/ensure",
		"GET /api/v1/identity",
		"GET /api/v1/coordination/capabilities",
		"GET /api/v1/identity",
		"POST /api/v1/sessions",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestSpawnSavedHostRejectsDestinationWithoutHostQualifiedParents(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-parent")
	var spawnWrites int
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/coordination/capabilities":
			_, _ = io.WriteString(w, `{"capabilities":["automation-authored"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			spawnWrites++
			_, _ = io.WriteString(w, `{"session":{"id":"wrong-parent"}}`)
		default:
			http.NotFound(w, r)
		}
	}))

	_, _, err := executeCLI(t, Deps{}, "spawn", "--host", testSavedHostLabel,
		"--project", "demo", "--agent", "codex", "--name", "Remote worker")
	if err == nil || !strings.Contains(err.Error(), "host-qualified-parent") {
		t.Fatalf("err = %v, want unsupported host-qualified-parent capability", err)
	}
	if spawnWrites != 0 {
		t.Fatalf("spawn writes = %d, want none", spawnWrites)
	}
}

func TestSpawnSavedHostUserShellDoesNotNeedSourceDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-source")
	t.Setenv("AO_SHELL_TERMINAL", "1")
	var request spawnRequest
	var capabilityRequests int
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case r.URL.Path == "/api/v1/coordination/capabilities":
			capabilityRequests++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	if err := os.Remove(os.Getenv("AO_RUN_FILE")); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := executeCLI(t, Deps{}, "spawn", "--host", testSavedHostLabel,
		"--project", "demo", "--agent", "codex", "--name", "Remote worker"); err != nil {
		t.Fatalf("user-shell spawn: %v stderr=%s", err, stderr)
	}
	if request.ParentSessionID != "" || request.ParentHostID != "" {
		t.Fatalf("parent = %q/%q, want none for a user-shell spawn", request.ParentHostID, request.ParentSessionID)
	}
	if capabilityRequests != 0 {
		t.Fatalf("capability requests = %d, want none for a user-shell spawn", capabilityRequests)
	}
}

func TestSpawnSavedHostFailureNamesRemoteRecoveryLocation(t *testing.T) {
	oldPollInterval := spawnStartPollInterval
	spawnStartPollInterval = 0
	t.Cleanup(func() { spawnStartPollInterval = oldPollInterval })

	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			_, _ = io.WriteString(w, `{"session":{"id":"remote-1","status":"spawning","provisionState":"provisioning"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/remote-1":
			_, _ = io.WriteString(w, `{"session":{"id":"remote-1","status":"failed","provisionState":"failed","provisionError":"setup failed"}}`)
		default:
			http.NotFound(w, r)
		}
	}))

	_, _, err := executeCLI(t, Deps{}, "spawn", "--host", testSavedHostLabel,
		"--project", "demo", "--agent", "codex", "--name", "Remote worker")
	if err == nil || !strings.Contains(err.Error(), "session remote-1 on desk (h_desk) failed to start") ||
		!strings.Contains(err.Error(), "open that machine in the Orchestrator app") {
		t.Fatalf("err = %v, want destination-qualified remote recovery guidance", err)
	}
	if strings.Contains(err.Error(), "ao session kill") {
		t.Fatalf("remote failure recommended a local-only kill command: %v", err)
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
				if r.URL.Path == "/api/v1/coordination/capabilities" {
					writeSavedHostCapabilities(t, w, r)
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
				if authored, _ := payload["automationAuthored"].(bool); !authored {
					t.Fatalf("remote write automationAuthored = %v, want true", payload["automationAuthored"])
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

func TestSendSavedHostUserShellIsHumanAuthored(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
		body string
	}{
		{name: "send", args: []string{"--message", "continue"}, path: "/api/v1/sessions/remote-1/send", body: `{"ok":true}`},
		{name: "steer", args: []string{"--steer", "--message", "continue"}, path: "/api/v1/sessions/remote-1/conversation/steer-or-send", body: `{"outcome":"steered","providerTurnId":"turn-1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "local-source")
			t.Setenv("AO_SHELL_TERMINAL", "1")
			var payload map[string]any
			var capabilityRequests int
			configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/identity" {
					writeSavedHostIdentity(t, w, r)
					return
				}
				if r.URL.Path == "/api/v1/coordination/capabilities" {
					capabilityRequests++
				}
				requireSavedHostRequest(t, r)
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					http.NotFound(w, r)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))

			args := append([]string{"send", "--host", testSavedHostLabel, "--session", "remote-1"}, tc.args...)
			if _, stderr, err := executeCLI(t, Deps{}, args...); err != nil {
				t.Fatalf("user-shell send: %v stderr=%s", err, stderr)
			}
			if capabilityRequests != 0 {
				t.Fatalf("capability requests = %d, want none for a human-authored shell command", capabilityRequests)
			}
			if payload["senderSessionId"] != nil || payload["automationAuthored"] != nil {
				t.Fatalf("payload = %v, want no agent provenance", payload)
			}
			messageKey := "message"
			if tc.name == "steer" {
				messageKey = "text"
			}
			if got := payload[messageKey]; got != "continue" {
				t.Fatalf("%s = %v, want unprefixed human message", messageKey, got)
			}
		})
	}
}

func TestSendSavedHostRecoveryKeepsTarget(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "")
			configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/identity" {
					writeSavedHostIdentity(t, w, r)
					return
				}
				requireSavedHostRequest(t, r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				code := "INTERNAL"
				if status == http.StatusConflict {
					code = "CHAT_STEER_UNCERTAIN"
				}
				_, _ = io.WriteString(w, `{"error":"failure","code":"`+code+`","message":"unknown"}`)
			}))

			_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel, "--session", "remote-1",
				"--steer", "--client-message-id", "delivery-1", "--message", "correct course")
			if err == nil {
				t.Fatal("send succeeded, want recovery command")
			}
			errText := recoveryErrorText(t, err.Error())
			if !recoveryArgumentPresent(errText, "--host", "h_desk") ||
				!strings.Contains(errText, "--recover-only") {
				t.Fatalf("err = %v, want host-qualified recovery command", err)
			}
		})
	}
}

func TestSendRawURLRecoveryKeepsTarget(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "")
			t.Setenv("AO_TOKEN", "raw-url-secret")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/identity" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"hostId":"h_raw","apiVersion":1}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				code := "INTERNAL"
				if status == http.StatusConflict {
					code = "CHAT_STEER_UNCERTAIN"
				}
				_, _ = io.WriteString(w, `{"error":"failure","code":"`+code+`","message":"unknown"}`)
			}))
			t.Cleanup(srv.Close)

			_, _, err := executeCLI(t, Deps{}, "send", "--url", srv.URL, "--session", "remote-1",
				"--steer", "--client-message-id", "delivery-1", "--message", "correct course")
			if err == nil {
				t.Fatal("send succeeded, want recovery command")
			}
			errText := recoveryErrorText(t, err.Error())
			if !recoveryArgumentPresent(errText, "--url", srv.URL) ||
				!strings.Contains(errText, "--recover-only") || strings.Contains(errText, "raw-url-secret") {
				t.Fatalf("err = %v, want URL-qualified credential-free recovery command", err)
			}
		})
	}
}

func TestRawURLHumanAndRecoveryRequestsPinIdentityAndRefuseRedirects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "human send", args: []string{"--message", "continue"}},
		{name: "receipt recovery", args: []string{"--steer", "--recover-only", "--client-message-id", "delivery-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_TOKEN", "raw-url-secret")
			var redirected, writes int
			sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				redirected++
			}))
			t.Cleanup(sink.Close)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/identity" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"hostId":"h_raw","apiVersion":1}`)
					return
				}
				writes++
				if got := r.Header.Get("Authorization"); got != "Bearer raw-url-secret" {
					t.Fatalf("Authorization = %q", got)
				}
				if got := r.Header.Get("X-AO-Expected-Host-ID"); got != "h_raw" {
					t.Fatalf("expected host = %q, want h_raw", got)
				}
				http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
			}))
			t.Cleanup(srv.Close)

			args := append([]string{"send", "--url", srv.URL, "--session", "remote-1"}, tc.args...)
			if _, _, err := executeCLI(t, Deps{}, args...); err == nil {
				t.Fatal("request followed a cross-origin redirect")
			}
			if writes != 1 || redirected != 0 {
				t.Fatalf("writes=%d redirected=%d, want one pinned write and no redirect", writes, redirected)
			}
		})
	}
}

func TestExistingRawURLCommandDoesNotRequireIdentity(t *testing.T) {
	t.Setenv("AO_TOKEN", "legacy-secret")
	var identityRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			identityRequests++
			http.NotFound(w, r)
		case "/api/v1/projects":
			_, _ = io.WriteString(w, `{"projects":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	if _, stderr, err := executeCLI(t, Deps{}, "project", "ls", "--url", srv.URL); err != nil {
		t.Fatalf("legacy raw-url project list: %v stderr=%s", err, stderr)
	}
	if identityRequests != 0 {
		t.Fatalf("identity requests = %d, want none for a preexisting raw-url command", identityRequests)
	}
}

func TestSteerPreflightFailureRecommendsOriginalRetry(t *testing.T) {
	var identityCalls, authenticatedRequests int
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			identityCalls++
			if identityCalls == 1 {
				writeSavedHostIdentity(t, w, r)
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		authenticatedRequests++
		http.NotFound(w, r)
	}))

	_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel, "--session", "remote-1",
		"--steer", "--client-message-id", "delivery-1", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "steering request was not sent") ||
		!strings.Contains(err.Error(), "retry the original command") ||
		!strings.Contains(err.Error(), "--client-message-id delivery-1") {
		t.Fatalf("err = %v, want definitely-not-sent retry guidance", err)
	}
	if strings.Contains(err.Error(), "--recover-only") {
		t.Fatalf("preflight failure incorrectly recommended receipt-only recovery: %v", err)
	}
	if authenticatedRequests != 0 {
		t.Fatalf("authenticated requests = %d, want none after failed identity preflight", authenticatedRequests)
	}
}

func TestSendSavedHostRecoverOnlyDoesNotNeedSourceDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-source")
	var request conversationMessageAPIRequest
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			writeSavedHostIdentity(t, w, r)
			return
		}
		requireSavedHostRequest(t, r)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"outcome":"steered","providerTurnId":"turn-1"}`)
	}))
	if err := os.Remove(os.Getenv("AO_RUN_FILE")); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel, "--session", "remote-1",
		"--steer", "--recover-only", "--client-message-id", "job 42"); err != nil {
		t.Fatalf("recover-only: %v stderr=%s", err, stderr)
	}
	if !request.RecoverOnly || request.ClientMessageID != "job 42" || request.SenderSessionID != "" || request.AutomationAuthored {
		t.Fatalf("request = %+v, want provenance-free receipt recovery", request)
	}
}

func TestSendSavedHostRejectsOldDestinationBeforeAgentAuthoredWrite(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-source")
	var writes int
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			writeSavedHostIdentity(t, w, r)
			return
		}
		requireSavedHostRequest(t, r)
		if r.Method == http.MethodPost {
			writes++
		}
		http.NotFound(w, r)
	}))

	_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel,
		"--session", "remote-1", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "automation provenance") {
		t.Fatalf("err = %v, want unsupported automation provenance", err)
	}
	if writes != 0 {
		t.Fatalf("agent-authored writes = %d, want none", writes)
	}
}

func TestSendSavedHostAgentSourceRequiresSelectedDaemon(t *testing.T) {
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
	if err := os.Remove(os.Getenv("AO_RUN_FILE")); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel, "--session", "remote-1", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "source session daemon") {
		t.Fatalf("err = %v, want unverifiable source daemon error", err)
	}
	if authenticatedWrites != 0 {
		t.Fatalf("authenticated writes = %d, want none", authenticatedWrites)
	}
}

func TestSendSavedHostRejectsRepointedSourceDaemonWithCollidingSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "local-source")
	var authenticatedDestinationRequests int
	configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			writeSavedHostIdentity(t, w, r)
			return
		}
		authenticatedDestinationRequests++
		http.NotFound(w, r)
	}))

	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_repointed","apiVersion":1}`)
		case "/api/v1/sessions/local-source":
			_, _ = io.WriteString(w, `{"session":{"id":"local-source"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(impostor.Close)
	writeRunFileFor(t, testConfig{runFile: os.Getenv("AO_RUN_FILE")}, impostor)

	_, _, err := executeCLI(t, Deps{}, "send", "--host", testSavedHostLabel,
		"--session", "remote-1", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "does not match this session's launch host") {
		t.Fatalf("err = %v, want fail-closed source launch identity mismatch", err)
	}
	if authenticatedDestinationRequests != 0 {
		t.Fatalf("authenticated destination requests = %d, want none", authenticatedDestinationRequests)
	}
}

func TestExplicitURLPinsIdentityBeforeSendingHostLocalIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "local-source")
	t.Setenv("AO_TOKEN", "same-daemon-token")
	identityDir := filepath.Join(cfg.dataDir, "mobile")
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "identity.json"), []byte(`{"hostId":"h_selected"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var identityCalls, authenticatedRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			identityCalls++
			hostID := "h_selected"
			if identityCalls >= 4 {
				hostID = "h_reassigned"
			}
			_, _ = io.WriteString(w, `{"hostId":"`+hostID+`","apiVersion":1}`)
		case "/api/v1/sessions/local-source":
			_, _ = io.WriteString(w, `{"session":{"id":"local-source"}}`)
		default:
			authenticatedRequests++
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{}, "send", "--url", srv.URL,
		"--session", "target", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "remote host identity changed") || !strings.Contains(err.Error(), "credential was not sent") {
		t.Fatalf("err = %v, want identity-change refusal", err)
	}
	if authenticatedRequests != 0 {
		t.Fatalf("authenticated requests = %d, want none after identity reassignment", authenticatedRequests)
	}
}

func TestExplicitURLToSelectedDaemonPreservesLocalLineageAndAttribution(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, serverURL string) error
	}{
		{
			name: "spawn",
			run: func(t *testing.T, serverURL string) error {
				_, _, err := executeCLI(t, Deps{}, "spawn", "--url", serverURL, "--project", "demo",
					"--agent", "codex", "--name", "Local child")
				return err
			},
		},
		{
			name: "send",
			run: func(t *testing.T, serverURL string) error {
				_, _, err := executeCLI(t, Deps{}, "send", "--url", serverURL, "--session", "target",
					"--message", "continue")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_SESSION_ID", "local-source")
			t.Setenv("AO_TOKEN", "same-daemon-token")
			identityDir := filepath.Join(cfg.dataDir, "mobile")
			if err := os.MkdirAll(identityDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(identityDir, "identity.json"), []byte(`{"hostId":"h_selected"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			captured := map[string]any{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/identity":
					_, _ = io.WriteString(w, `{"hostId":"h_selected","apiVersion":1}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/local-source":
					_, _ = io.WriteString(w, `{"session":{"id":"local-source"}}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
					_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","kind":"single-repo"}}`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
					_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
					if got := r.Header.Get("X-AO-Expected-Host-ID"); got != "h_selected" {
						t.Fatalf("expected host header = %q, want h_selected", got)
					}
					if got := r.Header.Get("X-AO-Caller-Session"); got != "local-source" {
						t.Fatalf("caller header = %q, want local-source", got)
					}
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatal(err)
					}
					_, _ = io.WriteString(w, `{"session":{"id":"child","status":"idle","displayName":"Local child"}}`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/target/send":
					if got := r.Header.Get("X-AO-Expected-Host-ID"); got != "h_selected" {
						t.Fatalf("expected host header = %q, want h_selected", got)
					}
					if got := r.Header.Get("X-AO-Caller-Session"); got != "local-source" {
						t.Fatalf("caller header = %q, want local-source", got)
					}
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatal(err)
					}
					_, _ = io.WriteString(w, `{"ok":true}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)

			if err := tc.run(t, srv.URL); err != nil {
				t.Fatal(err)
			}
			if got := captured["parentSessionId"]; tc.name == "spawn" && got != "local-source" {
				t.Fatalf("parentSessionId = %v, want local-source", got)
			}
			if tc.name == "send" {
				if got := captured["senderSessionId"]; got != "local-source" {
					t.Fatalf("senderSessionId = %v, want local-source", got)
				}
				if got := captured["automationAuthored"]; got != nil {
					t.Fatalf("automationAuthored = %v, want omitted for same daemon", got)
				}
				if got := captured["message"]; got != "[from local-source] continue" {
					t.Fatalf("message = %v, want local source attribution", got)
				}
			}
		})
	}
}

func TestRemoteRetryArgsPreservesTargetWithoutCredentials(t *testing.T) {
	t.Run("saved host", func(t *testing.T) {
		c := &commandContext{remote: &remoteTarget{source: "--host", hostID: testSavedHostID}}
		got := c.remoteRetryArguments()
		if len(got) != 2 || got[0] != "--host" || got[1] != testSavedHostID {
			t.Fatalf("remoteRetryArguments() = %q", got)
		}
	})
	t.Run("raw url", func(t *testing.T) {
		c := &commandContext{remote: &remoteTarget{source: "--url", baseURL: "https://desk.example/a path", token: "must-not-leak"}}
		got := c.remoteRetryArguments()
		if len(got) != 2 || got[0] != "--url" || got[1] != "https://desk.example/a path" || strings.Contains(strings.Join(got, " "), "must-not-leak") {
			t.Fatalf("remoteRetryArguments() = %q, want URL without credential", got)
		}
	})
}

func TestWindowsRecoveryCommandEncodesArbitraryArguments(t *testing.T) {
	c := &commandContext{remote: &remoteTarget{
		source: "--url", baseURL: "https://desk.example/$env:USERNAME/$(Get-Date)/owner's", token: "must-not-leak",
	}}
	command := c.steeringRecoveryCommandForOS("windows", "remote session", "job 42 %PATH% $(Get-Date)")
	if strings.Contains(command, "desk.example") || strings.Contains(command, "$env") || strings.Contains(command, "%PATH%") || strings.Contains(command, "must-not-leak") {
		t.Fatalf("encoded command exposed an interpolated argument or credential: %q", command)
	}
	script := decodePowerShellCommand(t, command)
	for _, want := range []string{
		"'--url' 'https://desk.example/$env:USERNAME/$(Get-Date)/owner''s'",
		"'--session' 'remote session'",
		"'--client-message-id' 'job 42 %PATH% $(Get-Date)'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("decoded script = %q, want %q", script, want)
		}
	}
	if strings.Contains(script, "must-not-leak") {
		t.Fatalf("decoded script leaked the bearer: %q", script)
	}
}

func TestRecoveryErrorTextDecodesEmbeddedWindowsCommand(t *testing.T) {
	command := (&commandContext{}).steeringRecoveryCommandForOS("windows", "remote-1", "delivery-1")
	got := recoveryErrorText(t, "request failed; recover without resending it: "+command)
	for _, want := range []string{"request failed", "'--session' 'remote-1'", "'--client-message-id' 'delivery-1'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recoveryErrorText() = %q, want %q", got, want)
		}
	}
}

func TestPOSIXRecoveryCommandQuotesLeadingEqualsHandle(t *testing.T) {
	c := &commandContext{remote: &remoteTarget{source: "--host", hostID: testSavedHostID}}
	command := c.steeringRecoveryCommandForOS("linux", "remote-1", "=git")
	if !strings.Contains(command, "--client-message-id '=git'") || strings.Contains(command, "--client-message-id =git") {
		t.Fatalf("POSIX recovery command did not quote leading equals handle: %q", command)
	}
}

func decodePowerShellCommand(t *testing.T, command string) string {
	t.Helper()
	const prefix = "powershell.exe -NoProfile -EncodedCommand "
	if !strings.HasPrefix(command, prefix) {
		t.Fatalf("Windows recovery command = %q, want encoded PowerShell", command)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(command, prefix))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("decode Windows recovery command: bytes=%d err=%v", len(raw), err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

func recoveryErrorText(t *testing.T, text string) string {
	t.Helper()
	const prefix = "powershell.exe -NoProfile -EncodedCommand "
	index := strings.Index(text, prefix)
	if index < 0 {
		return text
	}
	return text[:index] + decodePowerShellCommand(t, text[index:])
}

func recoveryArgumentPresent(text, name, value string) bool {
	return strings.Contains(text, name+" "+value) ||
		strings.Contains(text, "'"+strings.ReplaceAll(name, "'", "''")+"' '"+strings.ReplaceAll(value, "'", "''")+"'")
}

func TestSavedHostRejectsUnsafeHostIDBeforeRenderingCommands(t *testing.T) {
	home := aoHome(t)
	_ = setConfigEnv(t)
	t.Setenv("AO_DATA_DIR", "")
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "unsafe", URL: "http://127.0.0.1:9", HostID: "h_$(unsafe)", CoordinationToken: testCoordination,
	})

	_, _, err := executeCLI(t, Deps{}, "send", "--host", "unsafe", "--session", "remote-1", "--message", "continue")
	if err == nil || !strings.Contains(err.Error(), "invalid host ID") {
		t.Fatalf("err = %v, want invalid host ID before any connection", err)
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

	t.Run("spawn claim is rejected for a saved host", func(t *testing.T) {
		configureSavedHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/identity" {
				writeSavedHostIdentity(t, w, r)
				return
			}
			http.NotFound(w, r)
		}))
		_, _, err := executeCLI(t, Deps{}, "spawn", "--host", testSavedHostLabel, "--project", "demo", "--agent", "codex",
			"--name", "worker", "--claim-pr", "42")
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--claim-pr is not supported with --host") {
			t.Fatalf("err=%v exit=%d, want remote-claim usage error", err, ExitCode(err))
		}
	})
}
