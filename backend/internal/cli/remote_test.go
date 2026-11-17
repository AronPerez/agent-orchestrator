package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// aoHome points the AO home directory (and therefore remotes.json) at a temp
// dir, and clears the remote env vars so a developer's real AO_URL/AO_TOKEN
// cannot leak into a test.
func aoHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows
	t.Setenv("AO_URL", "")
	t.Setenv("AO_TOKEN", "")
	t.Setenv("AO_DATA_DIR", "")
	t.Setenv(remotesFileEnv, "")
	t.Setenv("AO_CLOUD_ACCOUNT_FILE", "")
	return dir
}

func writeRemotes(t *testing.T, home string, perm os.FileMode, entries ...remoteEntry) {
	t.Helper()
	raw, err := json.Marshal(remotesFile{Remotes: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ao"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ao", remotesFileName)
	if err := os.WriteFile(path, raw, perm); err != nil {
		t.Fatal(err)
	}
	// WriteFile honors umask, so force the mode the test asked for.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func writeActiveCloudAccount(t *testing.T, home, userID string) {
	t.Helper()
	path := filepath.Join(home, ".ao", "cloud_active_user")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if userID == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	raw, err := json.Marshal(activeCloudAccountMarker{UserID: userID, DesktopPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeRemoteURL(t *testing.T) {
	ok := map[string]string{
		"http://host:3011":         "http://host:3011",
		"http://host:3011/":        "http://host:3011",
		"host:3011":                "http://host:3011", // bare host defaults to plaintext http
		"https://box.ts.net":       "https://box.ts.net",
		"http://host:3011/ao/":     "http://host:3011/ao",
		"HTTP://Host:3011":         "http://Host:3011",
		"http://100.64.0.1:3011//": "http://100.64.0.1:3011",
	}
	for in, want := range ok {
		got, err := normalizeRemoteURL(in)
		if err != nil {
			t.Fatalf("normalizeRemoteURL(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("normalizeRemoteURL(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"ftp://host", "ws://host:3011", "http://", "://x"} {
		if got, err := normalizeRemoteURL(in); err == nil {
			t.Errorf("normalizeRemoteURL(%q) = %q, want error", in, got)
		}
	}

	// An "@" outside the authority is not userinfo and must still parse.
	for _, in := range []string{"http://host:3011/a@b", "http://host:3011/x?q=a@b"} {
		if _, err := normalizeRemoteURL(in); err != nil {
			t.Errorf("normalizeRemoteURL(%q): %v", in, err)
		}
	}
}

// A credential typed into the URL is rejected outright rather than silently
// dropped — and the error must never echo it back.
func TestNormalizeRemoteURLRejectsUserinfo(t *testing.T) {
	for _, in := range []string{
		"http://user:hunter2@host:3011",
		"https://user:hunter2@host:3011/path",
		"user:hunter2@host:3011", // scheme-less form
		"http://hunter2@host:3011",
	} {
		_, err := normalizeRemoteURL(in)
		if err == nil {
			t.Fatalf("normalizeRemoteURL(%q) succeeded, want a userinfo rejection", in)
		}
		if !strings.Contains(err.Error(), "must not carry a username or password") {
			t.Errorf("normalizeRemoteURL(%q) error = %q, want a userinfo rejection", in, err)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("normalizeRemoteURL(%q) echoed the credential: %q", in, err)
		}
	}
}

// The same rejection must reach the user through the real resolution path, with
// the credential still absent from the message.
func TestResolveRemoteTargetRejectsUserinfo(t *testing.T) {
	aoHome(t)
	_, err := resolveRemoteTarget("http://user:hunter2@host:3011")
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err = %v, want a rejection that does not echo the password", err)
	}
}

// No --url and no AO_URL must resolve to nil: that is what keeps every local
// invocation on the run-file path it has always used.
func TestResolveRemoteTargetDefaultsToLocal(t *testing.T) {
	aoHome(t)
	got, err := resolveRemoteTarget("")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("resolveRemoteTarget = %+v, want nil (local)", got)
	}
}

func TestResolveRemoteTargetCredentialSources(t *testing.T) {
	t.Run("AO_TOKEN wins over remotes.json", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600, remoteEntry{URL: "http://host:3011", Password: "fromfile"})
		t.Setenv("AO_TOKEN", "fromenv")

		got, err := resolveRemoteTarget("http://host:3011")
		if err != nil {
			t.Fatal(err)
		}
		if got.token != "fromenv" || got.baseURL != "http://host:3011" || got.source != "--url" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("remotes.json entry matched by URL", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600,
			remoteEntry{Label: "other", URL: "http://elsewhere:3011", Password: "nope"},
			// Stored with a trailing slash: matching is on the normalized URL.
			remoteEntry{Label: "desk", URL: "http://host:3011/", Password: "s3cret12"},
		)
		got, err := resolveRemoteTarget("host:3011")
		if err != nil {
			t.Fatal(err)
		}
		if got.token != "s3cret12" {
			t.Fatalf("token = %q, want s3cret12", got.token)
		}
	})

	t.Run("AO_URL when no flag", func(t *testing.T) {
		aoHome(t)
		t.Setenv("AO_URL", "http://host:3011")
		t.Setenv("AO_TOKEN", "tok")

		got, err := resolveRemoteTarget("")
		if err != nil {
			t.Fatal(err)
		}
		if got.source != "AO_URL" {
			t.Fatalf("source = %q, want AO_URL", got.source)
		}
	})

	t.Run("no credential is an error", func(t *testing.T) {
		aoHome(t)
		_, err := resolveRemoteTarget("http://host:3011")
		if err == nil || !strings.Contains(err.Error(), "AO_TOKEN") {
			t.Fatalf("err = %v, want a missing-credential error naming AO_TOKEN", err)
		}
	})

	t.Run("unknown host in remotes.json is an error", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600, remoteEntry{URL: "http://elsewhere:3011", Password: "nope"})
		_, err := resolveRemoteTarget("http://host:3011")
		if err == nil || !strings.Contains(err.Error(), "no connection password") {
			t.Fatalf("err = %v, want the missing-entry error", err)
		}
	})

	// An entry that exists but carries no password is a different problem from
	// no entry at all, and must not be told to add the entry it already has.
	t.Run("matching entry with an empty password says so", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600, remoteEntry{Label: "desk", URL: "http://host:3011", Password: ""})
		_, err := resolveRemoteTarget("http://host:3011")
		if err == nil {
			t.Fatal("want an error for an entry with an empty password")
		}
		if !strings.Contains(err.Error(), "has an empty password") {
			t.Fatalf("err = %q, want it to name the empty password", err)
		}
		if strings.Contains(err.Error(), "add an entry") {
			t.Fatalf("err = %q, must not tell the user to add an entry that already exists", err)
		}
	})
}

func TestResolveSavedHostTargetByExactLabelOrHostID(t *testing.T) {
	home := aoHome(t)
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "desk", URL: "http://host:3011/", HostID: "h_desk", CoordinationToken: "aoc_coordination"},
		remoteEntry{Label: "mini", URL: "https://mini.example", HostID: "h_mini", CoordinationToken: "aoc_mini"},
	)

	for _, name := range []string{"desk", "h_desk"} {
		got, err := resolveSavedHostTarget(name)
		if err != nil {
			t.Fatalf("resolveSavedHostTarget(%q): %v", name, err)
		}
		if got.baseURL != "http://host:3011" || got.token != "aoc_coordination" || got.hostID != "h_desk" || got.label != "desk" || got.source != "--host" {
			t.Fatalf("resolveSavedHostTarget(%q) = %+v", name, got)
		}
	}

	t.Run("stable host ID wins over a colliding label", func(t *testing.T) {
		writeRemotes(t, home, 0o600,
			remoteEntry{Label: "h_desk", URL: "http://other:3011", HostID: "h_other", CoordinationToken: "aoc_other"},
			remoteEntry{Label: "desk", URL: "http://host:3011", HostID: "h_desk", CoordinationToken: "aoc_coordination"},
		)
		got, err := resolveSavedHostTarget("h_desk")
		if err != nil {
			t.Fatal(err)
		}
		if got.hostID != "h_desk" || got.label != "desk" {
			t.Fatalf("got %+v, want the stable host ID match", got)
		}
	})
}

func TestResolveSavedHostTargetUsesDesktopCarriedRemotesFile(t *testing.T) {
	home := aoHome(t)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "desk", URL: "http://host:3011", HostID: "h_desk", CoordinationToken: "aoc_coordination",
	})
	// A packaged desktop runs its daemon and agents with ~/.ao/data as
	// AO_DATA_DIR, while the desktop-owned host store remains ~/.ao/remotes.json.
	t.Setenv("AO_DATA_DIR", filepath.Join(home, ".ao", "data"))
	t.Setenv("AO_REMOTES_FILE", filepath.Join(home, ".ao", remotesFileName))

	got, err := resolveSavedHostTarget("desk")
	if err != nil {
		t.Fatal(err)
	}
	if got.hostID != "h_desk" || got.token != "aoc_coordination" {
		t.Fatalf("resolved target = %+v", got)
	}
}

func TestSavedRemoteResolutionRespectsActiveAccount(t *testing.T) {
	home := aoHome(t)
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "desk", URL: "https://a.example", HostID: "h_a", CoordinationToken: "aoc_a", Password: "account-a", AccountUserID: "user-a"},
		remoteEntry{Label: "desk", URL: "https://b.example", HostID: "h_b", CoordinationToken: "aoc_b", Password: "account-b", AccountUserID: "user-b"},
		remoteEntry{Label: "local", URL: "https://local.example", HostID: "h_local", CoordinationToken: "aoc_local", Password: "local-password"},
	)

	for _, tc := range []struct {
		account string
		hostID  string
	}{
		{account: "user-a", hostID: "h_a"},
		{account: "user-b", hostID: "h_b"},
	} {
		got, err := resolveSavedHostTargetForAccount("desk", tc.account)
		if err != nil {
			t.Fatalf("account %q: %v", tc.account, err)
		}
		if got.hostID != tc.hostID {
			t.Fatalf("account %q selected %+v, want %s", tc.account, got, tc.hostID)
		}
	}
	if _, err := resolveSavedHostTargetForAccount("desk", ""); err == nil {
		t.Fatal("signed-out resolution exposed an account-owned host")
	}
	if got, err := resolveSavedHostTargetForAccount("local", ""); err != nil || got.hostID != "h_local" {
		t.Fatalf("signed-out local pairing = %+v, %v", got, err)
	}
	if _, err := resolveRemoteTargetForAccount("https://b.example", "user-a"); err == nil {
		t.Fatal("legacy --url lookup exposed another account's password")
	}
}

func TestSelectSavedHostUsesLocalActiveAccount(t *testing.T) {
	home := aoHome(t)
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "desk", URL: "https://a.example", HostID: "h_a", CoordinationToken: "aoc_a", AccountUserID: "user-a"},
		remoteEntry{Label: "desk", URL: "https://b.example", HostID: "h_b", CoordinationToken: "aoc_b", AccountUserID: "user-b"},
	)
	writeActiveCloudAccount(t, home, "user-a")
	switchAccountDuringProbe := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/identity":
			hostID := "h_a"
			if req.URL.Host == "b.example" {
				hostID = "h_b"
			}
			if switchAccountDuringProbe {
				writeActiveCloudAccount(t, home, "user-b")
			}
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"hostId":%q,"apiVersion":1}`, hostID)), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})}
	c := &commandContext{deps: Deps{HTTPClient: client, ProcessAlive: func(int) bool { return true }}.withDefaults()}
	if err := c.selectSavedHost(context.Background(), "desk"); err != nil || c.remote.hostID != "h_a" {
		t.Fatalf("user-a selection = %+v, %v", c.remote, err)
	}
	c.remote = nil
	writeActiveCloudAccount(t, home, "user-b")
	if err := c.selectSavedHost(context.Background(), "desk"); err != nil || c.remote.hostID != "h_b" {
		t.Fatalf("user-b selection = %+v, %v", c.remote, err)
	}
	c.remote = nil
	writeActiveCloudAccount(t, home, "")
	if err := c.selectSavedHost(context.Background(), "desk"); err == nil || c.remote != nil {
		t.Fatalf("signed-out selection = %+v, %v", c.remote, err)
	}
	writeActiveCloudAccount(t, home, "user-a")
	switchAccountDuringProbe = true
	if err := c.selectSavedHost(context.Background(), "desk"); err == nil || c.remote != nil {
		t.Fatalf("account-switch-during-probe selection = %+v, %v", c.remote, err)
	}
}

func TestResolveAllSavedHostTargetsRespectsAccountOwnership(t *testing.T) {
	home := aoHome(t)
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "local", URL: "https://local.example", HostID: "h_local", CoordinationToken: "aoc_local"},
		remoteEntry{Label: "owned-a", URL: "https://a.example", HostID: "h_a", CoordinationToken: "aoc_a", AccountUserID: "user-a"},
		remoteEntry{Label: "owned-b", URL: "https://b.example", HostID: "h_b", CoordinationToken: "aoc_b", AccountUserID: "user-b"},
		remoteEntry{Label: "needs-reconnect", URL: "https://broken.example", HostID: "h_broken"},
	)

	got, err := resolveAllSavedHostTargetsForAccount("user-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("candidates = %#v, want local, user-a, and invalid local pairing", got)
	}
	byID := make(map[string]savedHostTargetCandidate, len(got))
	for _, candidate := range got {
		byID[candidate.HostID] = candidate
	}
	if byID["h_a"].Target == nil || byID["h_local"].Target == nil {
		t.Fatalf("visible targets = %#v", got)
	}
	if _, leaked := byID["h_b"]; leaked {
		t.Fatalf("user-b host leaked into user-a candidates: %#v", got)
	}
	if candidate := byID["h_broken"]; candidate.Err == nil || candidate.Target != nil || !strings.Contains(candidate.Err.Error(), "coordination credential") {
		t.Fatalf("invalid candidate = %#v, want reconnect error without a target", candidate)
	}
}

func TestActiveCloudAccountUsesDesktopCarriedMarker(t *testing.T) {
	home := aoHome(t)
	writeActiveCloudAccount(t, home, "user-production")
	devDir := filepath.Join(home, ".ao", "dev")
	if err := os.MkdirAll(devDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(activeCloudAccountMarker{UserID: "user-development", DesktopPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	devMarker := filepath.Join(devDir, "cloud_active_user")
	if err := os.WriteFile(devMarker, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_CLOUD_ACCOUNT_FILE", devMarker)
	c := &commandContext{deps: Deps{ProcessAlive: func(int) bool { return true }}.withDefaults()}
	if got := c.activeCloudAccount(context.Background()); got != "user-development" {
		t.Fatalf("active account = %q, want development desktop account", got)
	}
}

func TestSavedHostResolverHidesCleanupRecords(t *testing.T) {
	home := aoHome(t)
	raw := `{"remotes":[{"label":"retired","url":"https://old.example","password":"old","hostId":"h_old","coordinationToken":"aoc_old","cleanupOnly":true}]}`
	path := filepath.Join(home, ".ao", remotesFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSavedHostTarget("retired"); err == nil {
		t.Fatal("cleanup-only row remained selectable")
	}
}

func TestSessionListUsesSavedHostThroughRootCommand(t *testing.T) {
	home := aoHome(t)
	var requests []string
	var sessionAuth, sessionExpectedHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/identity":
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Fatalf("identity probe Authorization = %q, want empty", auth)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "h_desk", "apiVersion": 1})
		case "/api/v1/sessions":
			sessionAuth = r.Header.Get("Authorization")
			sessionExpectedHost = r.Header.Get("X-AO-Expected-Host-ID")
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []any{}})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "desk", URL: srv.URL, HostID: "h_desk", CoordinationToken: "aoc_coordination",
	})

	var out bytes.Buffer
	err := executeWithDeps(Deps{Out: &out, Err: &out, HTTPClient: srv.Client()},
		[]string{"session", "ls", "--host", "desk"})
	if err != nil {
		t.Fatalf("ao session ls --host: %v\n%s", err, out.String())
	}
	if sessionAuth != "Bearer aoc_coordination" || sessionExpectedHost != "h_desk" {
		t.Fatalf("session request auth=%q expected-host=%q", sessionAuth, sessionExpectedHost)
	}
	if len(requests) < 2 || requests[0] != "GET /api/v1/identity" {
		t.Fatalf("requests = %v, want identity probe before authenticated reads", requests)
	}
}

func TestResolveSavedHostTargetRequiresUnambiguousCoordinationCredential(t *testing.T) {
	t.Run("full password is never a fallback", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600, remoteEntry{
			Label: "desk", URL: "http://host:3011", HostID: "h_desk", Password: "full-secret",
		})
		_, err := resolveSavedHostTarget("desk")
		if err == nil || !strings.Contains(err.Error(), "coordination credential") || !strings.Contains(err.Error(), "desktop") {
			t.Fatalf("err = %v, want reconnect guidance for a missing coordination credential", err)
		}
		if strings.Contains(err.Error(), "full-secret") {
			t.Fatalf("error exposed the full credential: %v", err)
		}
	})

	t.Run("legacy full-device token is rejected", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600, remoteEntry{
			Label: "desk", URL: "http://host:3011", HostID: "h_desk", CoordinationToken: "aod_legacy",
		})
		_, err := resolveSavedHostTarget("desk")
		if err == nil || !strings.Contains(err.Error(), "invalid coordination credential") {
			t.Fatalf("err = %v, want legacy token rejection", err)
		}
	})

	t.Run("host identity is required", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600, remoteEntry{Label: "desk", URL: "http://host:3011", CoordinationToken: "aoc_coordination"})
		_, err := resolveSavedHostTarget("desk")
		if err == nil || !strings.Contains(err.Error(), "host ID") {
			t.Fatalf("err = %v, want a missing-host-ID error", err)
		}
	})

	t.Run("duplicate exact labels are ambiguous", func(t *testing.T) {
		home := aoHome(t)
		writeRemotes(t, home, 0o600,
			remoteEntry{Label: "desk", URL: "http://one:3011", HostID: "h_one", CoordinationToken: "aoc_one"},
			remoteEntry{Label: "desk", URL: "http://two:3011", HostID: "h_two", CoordinationToken: "aoc_two"},
		)
		_, err := resolveSavedHostTarget("desk")
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("err = %v, want an ambiguity error", err)
		}
	})
}

func TestSelectSavedHostProbesIdentityBeforePresentingCredential(t *testing.T) {
	home := aoHome(t)
	var identityAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/identity" {
			t.Fatalf("path = %q, want identity probe", r.URL.Path)
		}
		identityAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "h_desk", "apiVersion": 1})
	}))
	defer srv.Close()
	writeRemotes(t, home, 0o600, remoteEntry{Label: "desk", URL: srv.URL, HostID: "h_desk", CoordinationToken: "aoc_coordination"})

	c := &commandContext{deps: Deps{HTTPClient: srv.Client()}.withDefaults()}
	if err := c.selectSavedHost(context.Background(), "desk"); err != nil {
		t.Fatal(err)
	}
	if identityAuth != "" {
		t.Fatalf("identity probe sent Authorization %q", identityAuth)
	}
	req := httptest.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions", nil)
	c.authorize(req)
	if got := req.Header.Get("Authorization"); got != "Bearer aoc_coordination" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("X-AO-Expected-Host-ID"); got != "h_desk" {
		t.Fatalf("X-AO-Expected-Host-ID = %q", got)
	}
}

func TestSavedHostAuthenticatedRequestsRejectRedirects(t *testing.T) {
	home := aoHome(t)
	var destinationAuth string
	destinationReached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationReached = true
		destinationAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []any{}})
	}))
	defer destination.Close()
	var originAuth string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/identity":
			_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "h_desk", "apiVersion": 1})
		case "/api/v1/sessions":
			originAuth = r.Header.Get("Authorization")
			http.Redirect(w, r, destination.URL+r.URL.Path, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "desk", URL: origin.URL, HostID: "h_desk", CoordinationToken: "aoc_coordination",
	})

	c := &commandContext{deps: Deps{HTTPClient: origin.Client()}.withDefaults()}
	if err := c.selectSavedHost(context.Background(), "desk"); err != nil {
		t.Fatal(err)
	}
	var sessions map[string]any
	err := c.getJSON(context.Background(), "sessions", &sessions)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirected saved-host request error = %v, want HTTP 302 refusal", err)
	}
	if originAuth != "Bearer aoc_coordination" {
		t.Fatalf("origin Authorization = %q", originAuth)
	}
	if destinationReached || destinationAuth != "" {
		t.Fatalf("redirect destination reached=%v Authorization=%q", destinationReached, destinationAuth)
	}
}

func TestSelectSavedHostRejectsIdentityMismatchAndSelfBeforeAuth(t *testing.T) {
	t.Run("saved identity changed", func(t *testing.T) {
		home := aoHome(t)
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "h_other", "apiVersion": 1})
		}))
		defer srv.Close()
		writeRemotes(t, home, 0o600, remoteEntry{Label: "desk", URL: srv.URL, HostID: "h_desk", CoordinationToken: "aoc_coordination"})
		c := &commandContext{deps: Deps{HTTPClient: srv.Client()}.withDefaults()}
		err := c.selectSavedHost(context.Background(), "desk")
		if err == nil || !strings.Contains(err.Error(), "identity changed") {
			t.Fatalf("err = %v, want identity mismatch", err)
		}
		if gotAuth != "" || c.remote != nil {
			t.Fatalf("credential was selected before identity proof: auth=%q remote=%+v", gotAuth, c.remote)
		}
	})

	t.Run("identity redirects are not followed", func(t *testing.T) {
		home := aoHome(t)
		followed := false
		destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
		defer destination.Close()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Fatalf("identity redirect response received Authorization %q", auth)
			}
			http.Redirect(w, r, destination.URL, http.StatusFound)
		}))
		defer srv.Close()
		writeRemotes(t, home, 0o600, remoteEntry{Label: "desk", URL: srv.URL, HostID: "h_desk", CoordinationToken: "aoc_coordination"})
		c := &commandContext{deps: Deps{HTTPClient: srv.Client()}.withDefaults()}
		err := c.selectSavedHost(context.Background(), "desk")
		if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
			t.Fatalf("err = %v, want redirect refusal", err)
		}
		if followed || c.remote != nil {
			t.Fatalf("identity redirect was followed=%v or selected remote=%+v", followed, c.remote)
		}
	})

	t.Run("saved host is this installation", func(t *testing.T) {
		home := aoHome(t)
		dataDir := filepath.Join(home, ".ao", "data")
		t.Setenv("AO_DATA_DIR", dataDir)
		identityPath := filepath.Join(dataDir, "mobile", "identity.json")
		if err := os.MkdirAll(filepath.Dir(identityPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(identityPath, []byte(`{"hostId":"h_self"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "h_self", "apiVersion": 1})
		}))
		defer srv.Close()
		// AO_DATA_DIR is also where an isolated desktop stores remotes.json.
		if err := os.WriteFile(filepath.Join(dataDir, remotesFileName), mustJSON(t, remotesFile{Remotes: []remoteEntry{{Label: "self", URL: srv.URL, HostID: "h_self", CoordinationToken: "aoc_coordination"}}}), 0o600); err != nil {
			t.Fatal(err)
		}
		c := &commandContext{deps: Deps{HTTPClient: srv.Client()}.withDefaults()}
		err := c.selectSavedHost(context.Background(), "self")
		if err == nil || !strings.Contains(err.Error(), "itself") {
			t.Fatalf("err = %v, want self-target refusal", err)
		}
		if gotAuth != "" || c.remote != nil {
			t.Fatalf("self probe presented a credential: auth=%q remote=%+v", gotAuth, c.remote)
		}
	})
}

func TestSelectSavedHostConflictsWithExplicitRemoteTarget(t *testing.T) {
	aoHome(t)
	c := &commandContext{remote: &remoteTarget{baseURL: "http://explicit:3011", source: "--url"}}
	err := c.selectSavedHost(context.Background(), "desk")
	var usage usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want a usage conflict", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// remotes.json holds connection passwords in plaintext, so a group/other
// readable file is refused rather than silently used.
func TestResolveRemoteTargetRejectsLooseRemotesFilePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows")
	}
	home := aoHome(t)
	writeRemotes(t, home, 0o644, remoteEntry{URL: "http://host:3011", Password: "s3cret12"})

	_, err := resolveRemoteTarget("http://host:3011")
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v, want a permissions refusal", err)
	}
}

// With no remote target, daemonBase must still go through the run-file and the
// local liveness gate — byte-identical to the pre-remote behavior.
func TestDaemonBaseLocalUsesRunFile(t *testing.T) {
	dir := aoHome(t)
	runFile := filepath.Join(dir, "running.json")
	t.Setenv("AO_RUN_FILE", runFile)
	t.Setenv("AO_DATA_DIR", filepath.Join(dir, "data"))

	c := &commandContext{deps: Deps{ProcessAlive: func(int) bool { return true }}.withDefaults()}

	if _, err := c.daemonBase(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v, want 'not running' with no run-file", err)
	}

	if err := runfile.Write(runFile, runfile.Info{PID: 4242, Port: 3999, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	base, err := c.daemonBase()
	if err != nil {
		t.Fatal(err)
	}
	if base != "http://127.0.0.1:3999" {
		t.Fatalf("base = %q, want the loopback daemon URL", base)
	}

	dead := &commandContext{deps: Deps{ProcessAlive: func(int) bool { return false }}.withDefaults()}
	if _, err := dead.daemonBase(); err == nil || !strings.Contains(err.Error(), "stale run-file") {
		t.Fatalf("err = %v, want the stale run-file error", err)
	}
}

// A remote target skips the run-file read and the local ProcessAlive gate
// entirely: neither exists on the machine running the command.
func TestDaemonBaseRemoteSkipsRunFile(t *testing.T) {
	dir := aoHome(t)
	t.Setenv("AO_RUN_FILE", filepath.Join(dir, "no-such-running.json"))

	c := &commandContext{
		deps:   Deps{ProcessAlive: func(int) bool { t.Fatal("ProcessAlive must not be consulted for a remote"); return false }}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "--url"},
	}
	base, err := c.daemonBase()
	if err != nil {
		t.Fatal(err)
	}
	if base != "http://host:3011" {
		t.Fatalf("base = %q", base)
	}
}

// `ao status --url ...` must work on a machine with no local run file at all,
// and must present the connection password as a Bearer token.
func TestStatusRemoteWithoutLocalRunFile(t *testing.T) {
	dir := aoHome(t)
	t.Setenv("AO_RUN_FILE", filepath.Join(dir, "no-such-running.json"))

	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		status := "ok"
		if r.URL.Path == "/readyz" {
			status = "ready"
		}
		_ = json.NewEncoder(w).Encode(probeResult{Status: status, Service: daemonmeta.ServiceName, PID: 777})
	}))
	defer srv.Close()

	t.Setenv("AO_TOKEN", "s3cret12")
	var out bytes.Buffer
	err := executeWithDeps(Deps{
		Out: &out,
		Err: &out,
		// A remote invocation must never consult local process liveness.
		ProcessAlive: func(int) bool { t.Error("ProcessAlive called for a remote target"); return false },
	}, []string{"status", "--url", srv.URL})
	if err != nil {
		t.Fatalf("ao status --url: %v\n%s", err, out.String())
	}

	if want := []string{"Bearer s3cret12", "Bearer s3cret12"}; len(gotAuth) != 2 || gotAuth[0] != want[0] || gotAuth[1] != want[1] {
		t.Fatalf("Authorization headers = %q, want %q", gotAuth, want)
	}
	for _, want := range []string{"Orchestrator daemon: ready", "url: " + srv.URL, "pid: 777"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, out.String())
		}
	}
	// There is no local run-file or data dir to report for a remote daemon.
	if strings.Contains(out.String(), "run file:") {
		t.Fatalf("remote status reported a local run file:\n%s", out.String())
	}
}

// A daemon that answers but is not ours must not be reported as a healthy AO.
func TestStatusRemoteRejectsForeignService(t *testing.T) {
	aoHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(probeResult{Status: "ok", Service: "something-else"})
	}))
	defer srv.Close()

	c := &commandContext{
		deps:   Deps{}.withDefaults(),
		remote: &remoteTarget{baseURL: srv.URL, token: "tok"},
	}
	st := c.inspectRemoteDaemon(context.Background())
	if st.State != stateUnhealthy || !strings.Contains(st.Error, "not from AO daemon") {
		t.Fatalf("state = %q, error = %q", st.State, st.Error)
	}
}

// A 429 from a remote daemon is the LAN listener's per-source lockout, not an
// unhealthy daemon. Reporting it as "unhealthy" inverts the diagnosis.
func TestStatusRemoteReportsLockoutDistinctly(t *testing.T) {
	aoHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := &commandContext{
		deps:   Deps{}.withDefaults(),
		remote: &remoteTarget{baseURL: srv.URL, token: "tok"},
	}
	st := c.inspectRemoteDaemon(context.Background())
	if st.State != stateLockedOut {
		t.Fatalf("state = %q, want %q", st.State, stateLockedOut)
	}
	if !strings.Contains(st.Error, "locked out") {
		t.Fatalf("error = %q, want it to name the lockout", st.Error)
	}
	// The whole point: it must not send the user to debug a healthy daemon.
	if strings.Contains(string(st.State), "unhealthy") || strings.Contains(st.Error, "HTTP 429") {
		t.Fatalf("lockout still rendered as a daemon fault: state=%q error=%q", st.State, st.Error)
	}
}

// ...and the LOCAL path must render the very same 429 exactly as before. The
// lockout cannot occur on loopback, so nothing about local status may change.
func TestStatusLocalUnchangedForSameHTTPStatus(t *testing.T) {
	dir := aoHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	runFile := filepath.Join(dir, "running.json")
	t.Setenv("AO_RUN_FILE", runFile)
	t.Setenv("AO_DATA_DIR", filepath.Join(dir, "data"))
	if err := runfile.Write(runFile, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	c := &commandContext{deps: Deps{
		ProcessAlive: func(int) bool { return true },
		Now:          func() time.Time { return time.Unix(200, 0).UTC() },
	}.withDefaults()}

	st, err := c.inspectDaemon(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != stateUnhealthy {
		t.Fatalf("local state = %q, want unhealthy (unchanged)", st.State)
	}
	if st.Error != "healthz: HTTP 429" {
		t.Fatalf("local error = %q, want the byte-identical %q", st.Error, "healthz: HTTP 429")
	}
}

// A stray --url must never shut down someone else's daemon.
func TestStopRefusesRemoteTarget(t *testing.T) {
	aoHome(t)
	c := &commandContext{
		deps:   Deps{ProcessAlive: func(int) bool { t.Fatal("stop must refuse before touching local state"); return false }}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "AO_URL"},
	}
	_, err := c.stopDaemon(context.Background(), stopOptions{})
	if err == nil {
		t.Fatal("ao stop --url must refuse")
	}
	for _, want := range []string{"refusing to stop a remote daemon", "AO_URL", "http://host:3011"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
	// The message must not assert anything about what the target exposes: a
	// loopback --url refuses too, and there /shutdown IS reachable.
	if strings.Contains(err.Error(), "/shutdown") {
		t.Fatalf("error %q claims something about the target's routes", err.Error())
	}
}

// The refusal is unconditional: a --url that happens to name loopback is still
// not the daemon `ao stop` discovered locally, and must not be stopped.
func TestStopRefusesLoopbackURLToo(t *testing.T) {
	aoHome(t)
	c := &commandContext{
		deps:   Deps{ProcessAlive: func(int) bool { t.Fatal("stop must refuse before touching local state"); return false }}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://127.0.0.1:3001", token: "tok", source: "--url"},
	}
	if _, err := c.stopDaemon(context.Background(), stopOptions{}); err == nil {
		t.Fatal("ao stop --url http://127.0.0.1:3001 must refuse")
	}
}

// CLI telemetry posts to loopback-only /internal routes, which are 404'd at the
// LAN socket — and must not be sent to another machine's daemon regardless.
func TestPostLoopbackJSONSkippedForRemote(t *testing.T) {
	aoHome(t)
	c := &commandContext{
		deps:   Deps{ProcessAlive: func(int) bool { t.Fatal("telemetry must not reach a remote daemon"); return false }}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok"},
	}
	if err := c.postLoopbackJSON(context.Background(), "/internal/telemetry/cli-invoked", map[string]string{}); err != nil {
		t.Fatal(err)
	}
}

// A host-relative path aimed at a remote daemon is refused, because the daemon
// resolves it against ITS OWN home/cwd — silently naming a directory on another
// machine. Measured: `ao project add --path '~/repo' --url <remote>` registers
// the remote host's ~/repo and never consults the operator's.
func TestCheckRemoteProjectPathRefusesHostRelative(t *testing.T) {
	aoHome(t)
	remote := &commandContext{
		deps:   Deps{}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "--url"},
	}
	for _, p := range []string{"~", "~/repo", "~user/repo", "repo", "./repo", "../repo"} {
		err := remote.checkRemoteProjectPath(p)
		if err == nil {
			t.Fatalf("checkRemoteProjectPath(%q) = nil, want a refusal", p)
		}
		if ExitCode(err) != 2 {
			t.Errorf("checkRemoteProjectPath(%q) exit code = %d, want 2 (usage)", p, ExitCode(err))
		}
		if !strings.Contains(err.Error(), "http://host:3011") {
			t.Errorf("refusal for %q does not name the target: %v", p, err)
		}
	}

	// Absolute paths stay allowed: they are meaningful on the remote host, and
	// refusing them would make a remote target useless. Every absolute FORM must
	// be accepted regardless of the OS running this test — the destination
	// filesystem is the remote daemon's, not this machine's. Using
	// filepath.IsAbs here shipped as a bug: it calls "/srv/repo" relative on
	// Windows, so a Windows CLI could not register a project on a Linux host.
	for _, p := range []string{
		"/srv/repo",        // POSIX absolute
		`C:\srv\repo`,      // Windows drive-absolute, backslash
		"C:/srv/repo",      // Windows drive-absolute, forward slash
		`\\server\share\r`, // Windows UNC
	} {
		if err := remote.checkRemoteProjectPath(p); err != nil {
			t.Errorf("absolute path %q refused: %v", p, err)
		}
	}

	// Still refused everywhere: forms that are host-relative even on Windows.
	for _, p := range []string{`C:repo`, `\repo`} {
		if err := remote.checkRemoteProjectPath(p); err == nil {
			t.Errorf("checkRemoteProjectPath(%q) = nil, want a refusal (drive/current-drive relative)", p)
		}
	}

	// Local behavior is untouched — every form above is still accepted.
	local := &commandContext{deps: Deps{}.withDefaults()}
	for _, p := range []string{"~", "~/repo", "repo", "./repo", "/srv/repo"} {
		if err := local.checkRemoteProjectPath(p); err != nil {
			t.Fatalf("local checkRemoteProjectPath(%q) = %v, want nil", p, err)
		}
	}
}

// The "registered project ... at <path>" echo is the only moment the operator
// can catch a wrong host, and for an absolute path the path alone is identical
// to what they typed. Name the daemon for a remote target; stay byte-identical
// for a local one.
func TestResolvedBySuffix(t *testing.T) {
	local := &commandContext{deps: Deps{}.withDefaults()}
	if got := local.resolvedBySuffix(); got != "" {
		t.Fatalf("local suffix = %q, want empty (output must not change)", got)
	}
	remote := &commandContext{
		deps:   Deps{}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "--url"},
	}
	if got := remote.resolvedBySuffix(); !strings.Contains(got, "http://host:3011") {
		t.Fatalf("remote suffix = %q, want it to name the daemon", got)
	}
}

// A local signal — AO_PROJECT_ID, AO_SESSION_ID or the current directory —
// must never select a project on a remote daemon. Measured on the base commit:
// `ao spawn --url <remote>` run inside an AO session inherited AO_PROJECT_ID
// and spawned a real session on the remote host, exit 0, no warning.
//
// Deliberately path-free so the assertion is identical on every runner OS.
func TestCheckRemoteImplicitProjectRefusesLocalSignals(t *testing.T) {
	newRemote := func() *commandContext {
		return &commandContext{
			deps:   Deps{}.withDefaults(),
			remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "--url"},
		}
	}
	for _, tc := range []struct {
		name       string
		projectID  string
		sessionID  string
		wantSignal string
	}{
		{name: "AO_PROJECT_ID", projectID: "demo", wantSignal: "AO_PROJECT_ID"},
		{name: "AO_SESSION_ID", sessionID: "demo-1", wantSignal: "AO_SESSION_ID"},
		{name: "cwd", wantSignal: "the current directory"},
		// An id that is only whitespace is not a signal — resolution would fall
		// through to the next one, so the refusal must name that one instead.
		{name: "blank AO_PROJECT_ID falls through", projectID: " \t ", sessionID: "demo-1", wantSignal: "AO_SESSION_ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_PROJECT_ID", tc.projectID)
			t.Setenv("AO_SESSION_ID", tc.sessionID)
			err := newRemote().checkRemoteImplicitProject("")
			if err == nil {
				t.Fatal("checkRemoteImplicitProject = nil, want a refusal")
			}
			if ExitCode(err) != 2 {
				t.Errorf("exit code = %d, want 2 (usage)", ExitCode(err))
			}
			for _, want := range []string{tc.wantSignal, "--url", "http://host:3011", "--project"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not mention %q: %v", want, err)
				}
			}
		})
	}

	// An explicit --project is the one input that means the same on both hosts.
	t.Setenv("AO_PROJECT_ID", "demo")
	if err := newRemote().checkRemoteImplicitProject("other"); err != nil {
		t.Errorf("explicit --project refused: %v", err)
	}

	// Local behavior is untouched: every implicit signal is still accepted.
	local := &commandContext{deps: Deps{}.withDefaults()}
	for _, id := range []string{"", "demo"} {
		if err := local.checkRemoteImplicitProject(id); err != nil {
			t.Errorf("local checkRemoteImplicitProject(%q) = %v, want nil", id, err)
		}
	}
}

// End to end: the refusal must land BEFORE any request, so a wrong-machine
// spawn cannot happen at all. The empty request log is the real assertion —
// a session created on the wrong host is undetectable from the output.
func TestSpawnRemoteRefusesInheritedProjectEnv(t *testing.T) {
	aoHome(t)
	t.Setenv("AO_SESSION_ID", "")
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("AO_TOKEN", "s3cret12")
	t.Setenv("AO_PROJECT_ID", "demo")

	var out bytes.Buffer
	err := executeWithDeps(Deps{Out: &out, Err: &out, ProcessAlive: func(int) bool { return true }},
		[]string{"spawn", "--url", srv.URL, "--name", "worker", "--prompt", "hi"})
	if err == nil {
		t.Fatalf("spawn --url with an inherited AO_PROJECT_ID succeeded: %s", out.String())
	}
	if ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "AO_PROJECT_ID") || !strings.Contains(err.Error(), "--project") {
		t.Errorf("error does not name the signal and the remedy: %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("refused spawn still contacted the remote daemon: %v", requests)
	}
}

// refuseLocalOnly is the whole point of the local-only refusals: the operator
// must be able to see, from the message alone, which flag pointed them off-box
// and where it pointed. Exit code 2, matching #50 and #56.
func TestRefuseLocalOnlyNamesFlagAndURL(t *testing.T) {
	local := &commandContext{deps: Deps{}.withDefaults()}
	if err := local.refuseLocalOnly("ao doctor", "is local"); err != nil {
		t.Fatalf("local refuseLocalOnly = %v, want nil (local behavior must not change)", err)
	}
	if err := local.refuseDaemonURLFlag(); err != nil {
		t.Fatalf("local refuseDaemonURLFlag = %v, want nil", err)
	}

	for _, source := range []string{"--url", "AO_URL"} {
		remote := &commandContext{
			deps:   Deps{}.withDefaults(),
			remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: source},
		}
		err := remote.refuseLocalOnly("ao doctor", "probes this machine")
		if err == nil {
			t.Fatalf("source %s: refuseLocalOnly = nil, want a refusal", source)
		}
		for _, want := range []string{source, "http://host:3011", "ao doctor", "probes this machine"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("source %s: error %q does not name %q", source, err, want)
			}
		}
		if got := ExitCode(err); got != 2 {
			t.Errorf("source %s: exit code = %d, want 2 (usage)", source, got)
		}
	}

	// `ao daemon` alone refuses the flag and ignores the environment variable:
	// it is spawned by the desktop app, so an exported AO_URL must not brick it.
	flagged := &commandContext{
		deps:   Deps{}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "--url"},
	}
	if err := flagged.refuseDaemonURLFlag(); err == nil {
		t.Error("refuseDaemonURLFlag with --url = nil, want a refusal")
	}
	exported := &commandContext{
		deps:   Deps{}.withDefaults(),
		remote: &remoteTarget{baseURL: "http://host:3011", token: "tok", source: "AO_URL"},
	}
	if err := exported.refuseDaemonURLFlag(); err != nil {
		t.Errorf("refuseDaemonURLFlag with AO_URL = %v, want nil (must not brick a spawned daemon)", err)
	}
}

// End to end: each command that acts only on this machine must refuse --url
// before it reaches the network. The real assertion is the EMPTY request log —
// `ao preview --url` reaching a remote daemon sets a preview target on a session
// that is not the operator's, and nothing in its output would say so.
//
// Deliberately path-free (no filepath, no t.Chdir, no absolute-path literals):
// #50 shipped a Windows-only break by judging a path with filepath.IsAbs, so
// these assert identically on every runner OS.
func TestLocalOnlyCommandsRefuseRemoteTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"doctor", []string{"doctor"}},
		{"preview", []string{"preview", "README.md"}},
		{"preview clear", []string{"preview", "clear"}},
		{"preview status", []string{"preview", "status"}},
		{"import", []string{"import", "--yes"}},
		{"start", []string{"start"}},
		{"daemon", []string{"daemon"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aoHome(t)
			setConfigEnv(t)
			// Set, to prove the refusal beats the local session id rather than
			// falling out of preview's "not inside a session" error.
			t.Setenv("AO_SESSION_ID", "local-1")
			t.Setenv("AO_TOKEN", "tok")

			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)

			_, _, err := executeCLI(t, Deps{}, append(tc.args, "--url", srv.URL)...)
			if err == nil {
				t.Fatalf("ao %s --url succeeded, want a refusal", strings.Join(tc.args, " "))
			}
			if !strings.Contains(err.Error(), "targets the remote daemon at "+srv.URL) {
				t.Fatalf("error = %v, want it to name --url and %s", err, srv.URL)
			}
			if got := ExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2 (usage)", got)
			}
			if requests != 0 {
				t.Errorf("%d request(s) reached the remote daemon, want 0", requests)
			}
		})
	}
}

// Local behavior is untouched: with no remote target, every one of these
// commands must fail (or not) for exactly the reasons it did before.
func TestLocalOnlyCommandsUnchangedWithoutRemoteTarget(t *testing.T) {
	aoHome(t)
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")

	// preview still reports the missing session id, not a remote refusal.
	_, _, err := executeCLI(t, Deps{}, "preview", "README.md")
	if err == nil || !strings.Contains(err.Error(), "AO_SESSION_ID is not set") {
		t.Fatalf("local ao preview error = %v, want the missing-session-id usage error", err)
	}

	// doctor still runs its checks. Which ones pass depends on the runner, so
	// the assertion is only that it did not refuse and did print a report.
	out, _, err := executeCLI(t, Deps{}, "doctor")
	if err != nil && strings.Contains(err.Error(), "targets the remote daemon") {
		t.Fatalf("local ao doctor refused: %v", err)
	}
	if !strings.Contains(out, "Core:") {
		t.Fatalf("local ao doctor output = %q, want the Core section", out)
	}
}
