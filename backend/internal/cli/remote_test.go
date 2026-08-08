package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
		if _, err := resolveRemoteTarget("http://host:3011"); err == nil {
			t.Fatal("want an error when no entry matches")
		}
	})
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
	for _, want := range []string{"AO daemon: ready", "url: " + srv.URL, "pid: 777"} {
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
