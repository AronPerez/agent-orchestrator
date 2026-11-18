package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func prepareAllHostsTest(t *testing.T, localSessionID string) (string, testConfig) {
	t.Helper()
	home := aoHome(t)
	cfg := setConfigEnv(t)
	// The desktop's ordinary store is ~/.ao/remotes.json while its daemon data
	// is ~/.ao/data. Keep that production layout and isolate only the run file.
	t.Setenv("AO_DATA_DIR", "")
	identityDir := filepath.Join(home, ".ao", "data", "mobile")
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "identity.json"), []byte(`{"hostId":"h_local"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(allHostsHandler(t, "h_local", localSessionID, ""))
	t.Cleanup(local.Close)
	writeRunFileFor(t, cfg, local)
	return home, cfg
}

func allHostsHandler(t *testing.T, hostID, sessionID, token string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("%s identity Authorization = %q, want empty", hostID, got)
			}
			_, _ = io.WriteString(w, `{"hostId":"`+hostID+`","apiVersion":1}`)
		case "/api/v1/sessions":
			if token != "" {
				if got := r.Header.Get("Authorization"); got != "Bearer "+token {
					t.Errorf("%s sessions Authorization = %q", hostID, got)
				}
				if got := r.Header.Get("X-AO-Expected-Host-ID"); got != hostID {
					t.Errorf("%s expected host = %q", hostID, got)
				}
			}
			if sessionID == "" {
				_, _ = io.WriteString(w, `{"sessions":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"sessions":[`+sessionJSON(sessionID, "demo", "worker", "idle", false)+`]}`)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestSessionListAllHostsJSONQualifiesAndSortsEverySession(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "local-1")
	alpha := httptest.NewServer(allHostsHandler(t, "h_alpha", "alpha-1", "aoc_alpha"))
	zeta := httptest.NewServer(allHostsHandler(t, "h_zeta", "zeta-1", "aoc_zeta"))
	t.Cleanup(alpha.Close)
	t.Cleanup(zeta.Close)
	// Deliberately store remotes out of display order. Completion order and file
	// order must not change the machine-qualified JSON contract.
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "zeta", URL: zeta.URL, HostID: "h_zeta", CoordinationToken: "aoc_zeta"},
		remoteEntry{Label: "alpha", URL: alpha.URL, HostID: "h_alpha", CoordinationToken: "aoc_alpha"},
	)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out)
	}
	if len(got.Data) != 3 {
		t.Fatalf("data = %#v, want three sessions", got.Data)
	}
	wantAddresses := []string{"h_local/local-1", "h_alpha/alpha-1", "h_zeta/zeta-1"}
	for i, entry := range got.Data {
		if address := entry.HostID + "/" + entry.ID; address != wantAddresses[i] {
			t.Fatalf("data[%d] address = %q, want %q; data=%#v", i, address, wantAddresses[i], got.Data)
		}
		if entry.HostLabel == "" {
			t.Fatalf("data[%d] has no host label: %#v", i, entry)
		}
	}
	if len(got.Meta.Hosts) != 3 {
		t.Fatalf("hosts = %#v, want local plus two remotes", got.Meta.Hosts)
	}
	for _, host := range got.Meta.Hosts {
		if host.Error != "" || host.SessionCount != 1 {
			t.Fatalf("host status = %#v, want one healthy session", host)
		}
	}
}

func TestSessionListAllHostsKeepsHealthyMachinesOnPartialFailure(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "local-1")
	good := httptest.NewServer(allHostsHandler(t, "h_good", "good-1", "aoc_good"))
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(good.Close)
	t.Cleanup(bad.Close)
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "asleep", URL: bad.URL, HostID: "h_sleep", CoordinationToken: "aoc_sleep"},
		remoteEntry{Label: "build", URL: good.URL, HostID: "h_good", CoordinationToken: "aoc_good"},
	)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated")
	if err != nil {
		t.Fatalf("partial multi-host list returned an error: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"this machine (h_local)", "build (h_good)", "local-1", "good-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "asleep (h_sleep)") || !strings.Contains(errOut, "unavailable") {
		t.Fatalf("stderr = %q, want machine-qualified partial-failure warning", errOut)
	}

	jsonOut, jsonErrOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("partial JSON list returned an error: %v\nstderr=%s", err, jsonErrOut)
	}
	var decoded sessionListOutput
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatalf("decode partial JSON: %v\n%s", err, jsonOut)
	}
	var sleeping *sessionListHostStatus
	for i := range decoded.Meta.Hosts {
		if decoded.Meta.Hosts[i].HostID == "h_sleep" {
			sleeping = &decoded.Meta.Hosts[i]
			break
		}
	}
	if sleeping == nil || sleeping.Error == "" {
		t.Fatalf("hosts = %#v, want structured h_sleep failure", decoded.Meta.Hosts)
	}
}

func TestSessionListAllHostsReadsLocalIdentityFromDaemonWhenFileIsMissing(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "local-1")
	if err := os.Remove(filepath.Join(home, ".ao", "data", "mobile", "identity.json")); err != nil {
		t.Fatal(err)
	}
	writeRemotes(t, home, 0o600)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].HostID != "h_local" {
		t.Fatalf("data = %#v, want daemon-derived local host identity", got.Data)
	}
}

func TestSessionListAllHostsUsesQueriedDaemonIdentityWhenLocalFileDisagrees(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "local-1")
	identityPath := filepath.Join(home, ".ao", "data", "mobile", "identity.json")
	if err := os.WriteFile(identityPath, []byte(`{"hostId":"h_remote"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(allHostsHandler(t, "h_remote", "remote-1", "aoc_remote"))
	t.Cleanup(remote.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "remote", URL: remote.URL, HostID: "h_remote", CoordinationToken: "aoc_remote",
	})

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 2 || got.Data[0].HostID != "h_local" || got.Data[1].HostID != "h_remote" {
		t.Fatalf("data = %#v, want queried local identity plus saved remote", got.Data)
	}
}

func TestSessionListAllHostsBoundsSnapshotAfterIdentitySucceeds(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "local-1")
	oldTimeout := allHostsSnapshotTimeout
	allHostsSnapshotTimeout = 250 * time.Millisecond
	t.Cleanup(func() { allHostsSnapshotTimeout = oldTimeout })
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_stalled","apiVersion":1}`)
		case "/api/v1/sessions":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stalled.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "stalled", URL: stalled.URL, HostID: "h_stalled", CoordinationToken: "aoc_stalled",
	})

	started := time.Now()
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("all-host list took %s, want stalled host bounded", elapsed)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].HostID != "h_local" {
		t.Fatalf("data = %#v, want completed local result", got.Data)
	}
	if len(got.Meta.Hosts) != 2 || got.Meta.Hosts[1].HostID != "h_stalled" || got.Meta.Hosts[1].Error == "" {
		t.Fatalf("hosts = %#v, want structured stalled-host failure", got.Meta.Hosts)
	}
}

func TestSessionListAllHostsDoesNotBlockRemotesOnStalledLocalIdentity(t *testing.T) {
	home := aoHome(t)
	cfg := setConfigEnv(t)
	t.Setenv("AO_DATA_DIR", "")

	oldTimeout := allHostsSnapshotTimeout
	allHostsSnapshotTimeout = 150 * time.Millisecond
	t.Cleanup(func() { allHostsSnapshotTimeout = oldTimeout })

	releaseLocal := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseLocal) }) })
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/identity" {
			http.NotFound(w, r)
			return
		}
		select {
		case <-releaseLocal:
			_, _ = io.WriteString(w, `{"hostId":"h_local","apiVersion":1}`)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(local.Close)
	writeRunFileFor(t, cfg, local)

	remoteStarted := make(chan struct{}, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			select {
			case remoteStarted <- struct{}{}:
			default:
			}
		}
		allHostsHandler(t, "h_remote", "remote-1", "aoc_remote").ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "remote", URL: remote.URL, HostID: "h_remote", CoordinationToken: "aoc_remote",
	})

	type cliResult struct {
		out, errOut string
		err         error
	}
	done := make(chan cliResult, 1)
	go func() {
		out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
			"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
		done <- cliResult{out: out, errOut: errOut, err: err}
	}()

	select {
	case <-remoteStarted:
	case <-time.After(300 * time.Millisecond):
		releaseOnce.Do(func() { close(releaseLocal) })
		result := <-done
		t.Fatalf("saved-host discovery waited for local identity; err=%v stderr=%s", result.err, result.errOut)
	}

	var result cliResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		releaseOnce.Do(func() { close(releaseLocal) })
		t.Fatal("session ls did not finish after the local identity deadline")
	}
	if result.err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", result.err, result.errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(result.out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].HostID != "h_remote" {
		t.Fatalf("data = %#v, want healthy remote result despite stalled local identity", got.Data)
	}
}

func TestSessionListAllHostsPinsLocalSnapshotToIdentityDaemon(t *testing.T) {
	home := aoHome(t)
	cfg := setConfigEnv(t)
	t.Setenv("AO_DATA_DIR", "")
	writeRemotes(t, home, 0o600)

	var replacementSessionRequests atomic.Int32
	replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_replacement","apiVersion":1}`)
		case "/api/v1/sessions":
			replacementSessionRequests.Add(1)
			_, _ = io.WriteString(w, `{"sessions":[`+sessionJSON("replacement-1", "demo", "worker", "idle", false)+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(replacement.Close)
	replacementPort := serverPort(t, replacement.URL)

	var identityRequests atomic.Int32
	var switchErr atomic.Value
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			if identityRequests.Add(1) == 1 {
				if err := runfile.Write(cfg.runFile, runfile.Info{
					PID: os.Getpid(), Port: replacementPort, StartedAt: time.Unix(100, 0).UTC(),
				}); err != nil {
					switchErr.Store(err)
				}
			}
			_, _ = io.WriteString(w, `{"hostId":"h_original","apiVersion":1}`)
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[`+sessionJSON("original-1", "demo", "worker", "idle", false)+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(original.Close)
	writeRunFileFor(t, cfg, original)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	if stored := switchErr.Load(); stored != nil {
		t.Fatalf("switch run file: %v", stored)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].HostID != "h_original" || got.Data[0].ID != "original-1" {
		t.Fatalf("data = %#v, want snapshot pinned to h_original", got.Data)
	}
	if got := replacementSessionRequests.Load(); got != 0 {
		t.Fatalf("replacement daemon received %d session request(s), want none", got)
	}
}

func TestSessionListAllHostsRevalidatesAccountDuringSnapshot(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "local-1")
	writeActiveCloudAccount(t, home, "user-a")
	startedAt, ok := processStartedAt(context.Background(), os.Getpid())
	if !ok {
		t.Fatal("could not read test process start time")
	}
	markerB, err := json.Marshal(activeCloudAccountMarker{
		UserID: "user-b", DesktopPID: os.Getpid(), DesktopStartedAtMS: startedAt.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(home, ".ao", activeCloudAccountFile)

	var sessionRequests atomic.Int32
	var switchErr atomic.Value
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_account","apiVersion":1}`)
		case "/api/v1/sessions":
			if got := r.Header.Get("Authorization"); got != "Bearer aoc_account" {
				t.Errorf("sessions Authorization = %q", got)
			}
			requestNumber := sessionRequests.Add(1)
			if requestNumber == 1 {
				_, _ = io.WriteString(w, `{"sessions":[]`)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				if err := os.WriteFile(markerPath, append(markerB, '\n'), 0o600); err != nil {
					switchErr.Store(err)
				}
				_, _ = io.WriteString(w, `}`)
				return
			}
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "account", URL: remote.URL, HostID: "h_account", CoordinationToken: "aoc_account", AccountUserID: "user-a",
	})

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	if stored := switchErr.Load(); stored != nil {
		t.Fatalf("switch active Cloud account: %v", stored)
	}
	if got := sessionRequests.Load(); got != 1 {
		t.Fatalf("remote received %d authenticated session requests, want one before the account switch", got)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].HostID != "h_local" {
		t.Fatalf("data = %#v, want only the local snapshot after account switch", got.Data)
	}
	if len(got.Meta.Hosts) != 2 || got.Meta.Hosts[1].HostID != "h_account" || !strings.Contains(got.Meta.Hosts[1].Error, "Cloud account changed") {
		t.Fatalf("hosts = %#v, want structured account-switch failure", got.Meta.Hosts)
	}
	if got.Meta.Hosts[1].SessionCount != 0 {
		t.Fatalf("rejected host session count = %d, want 0", got.Meta.Hosts[1].SessionCount)
	}
}

func TestSessionListAllHostsKeepsSessionsWhenPREnrichmentTimesOut(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "")
	oldEnrichmentTimeout := allHostsPREnrichmentTimeout
	allHostsPREnrichmentTimeout = 50 * time.Millisecond
	t.Cleanup(func() { allHostsPREnrichmentTimeout = oldEnrichmentTimeout })

	var prRequests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_pr","apiVersion":1}`)
		case "/api/v1/sessions":
			session := strings.TrimSuffix(sessionJSON("pr-1", "demo", "worker", "idle", false), "}") + `,"prs":[{"number":7}]}`
			_, _ = io.WriteString(w, `{"sessions":[`+session+`]}`)
		case "/api/v1/sessions/pr-1/pr":
			prRequests.Add(1)
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "review", URL: remote.URL, HostID: "h_pr", CoordinationToken: "aoc_pr",
	})

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	if got := prRequests.Load(); got != 1 {
		t.Fatalf("PR summary requests = %d, want 1", got)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].HostID != "h_pr" || got.Data[0].ID != "pr-1" {
		t.Fatalf("data = %#v, want the fetched session despite stalled optional enrichment", got.Data)
	}
	if len(got.Meta.Hosts) != 2 || got.Meta.Hosts[1].Error != "" || got.Meta.Hosts[1].SessionCount != 1 {
		t.Fatalf("hosts = %#v, want a healthy remote snapshot", got.Meta.Hosts)
	}
}

func TestSessionListAllHostsRejectsIdentityChangeAfterSnapshot(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "")
	var identityRequests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			hostID := "h_remote"
			if identityRequests.Add(1) >= 3 {
				hostID = "h_replacement"
			}
			_, _ = io.WriteString(w, `{"hostId":`+jsonQuote(hostID)+`,"apiVersion":1}`)
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[`+sessionJSON("remote-1", "demo", "worker", "idle", false)+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)
	writeRemotes(t, home, 0o600, remoteEntry{
		Label: "remote", URL: remote.URL, HostID: "h_remote", CoordinationToken: "aoc_remote",
	})

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 0 {
		t.Fatalf("data = %#v, want changed-host snapshot rejected", got.Data)
	}
	if len(got.Meta.Hosts) != 2 || got.Meta.Hosts[1].HostID != "h_remote" || !strings.Contains(got.Meta.Hosts[1].Error, "identity changed") || got.Meta.Hosts[1].SessionCount != 0 {
		t.Fatalf("hosts = %#v, want changed-host failure with no published count", got.Meta.Hosts)
	}
}

func TestSessionListAllHostsRevalidatesAccountAtPublication(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "")
	writeActiveCloudAccount(t, home, "user-a")
	startedAt, ok := processStartedAt(context.Background(), os.Getpid())
	if !ok {
		t.Fatal("could not read test process start time")
	}
	markerB, err := json.Marshal(activeCloudAccountMarker{
		UserID: "user-b", DesktopPID: os.Getpid(), DesktopStartedAtMS: startedAt.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(home, ".ao", activeCloudAccountFile)

	accountFinished := make(chan struct{})
	var accountFinishedOnce sync.Once
	var accountIdentityRequests atomic.Int32
	account := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_account","apiVersion":1}`)
			if accountIdentityRequests.Add(1) >= 3 {
				accountFinishedOnce.Do(func() { close(accountFinished) })
			}
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[`+sessionJSON("account-1", "demo", "worker", "idle", false)+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(account.Close)

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var slowStartedOnce, releaseSlowOnce sync.Once
	t.Cleanup(func() { releaseSlowOnce.Do(func() { close(releaseSlow) }) })
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/identity":
			_, _ = io.WriteString(w, `{"hostId":"h_slow","apiVersion":1}`)
		case "/api/v1/sessions":
			slowStartedOnce.Do(func() { close(slowStarted) })
			<-releaseSlow
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(slow.Close)
	writeRemotes(t, home, 0o600,
		remoteEntry{Label: "account", URL: account.URL, HostID: "h_account", CoordinationToken: "aoc_account", AccountUserID: "user-a"},
		remoteEntry{Label: "slow", URL: slow.URL, HostID: "h_slow", CoordinationToken: "aoc_slow"},
	)

	type cliResult struct {
		out, errOut string
		err         error
	}
	done := make(chan cliResult, 1)
	go func() {
		out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
			"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
		done <- cliResult{out: out, errOut: errOut, err: err}
	}()
	select {
	case <-accountFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("account-owned snapshot did not finish")
	}
	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow host did not start")
	}
	if err := os.WriteFile(markerPath, append(markerB, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseSlowOnce.Do(func() { close(releaseSlow) })
	result := <-done
	if result.err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", result.err, result.errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(result.out), &got); err != nil {
		t.Fatal(err)
	}
	for _, entry := range got.Data {
		if entry.HostID == "h_account" {
			t.Fatalf("account-owned snapshot published after account switch: %#v", got.Data)
		}
	}
	for _, host := range got.Meta.Hosts {
		if host.HostID == "h_account" {
			if !strings.Contains(host.Error, "before publishing") || host.SessionCount != 0 {
				t.Fatalf("account host status = %#v, want publication failure with no count", host)
			}
			return
		}
	}
	t.Fatalf("hosts = %#v, want account-owned host status", got.Meta.Hosts)
}

func TestSessionListAllHostsKeepsLocalResultWhenRemoteDiscoveryFails(t *testing.T) {
	for _, tc := range []struct {
		name       string
		breakStore func(t *testing.T, home string)
	}{
		{name: "corrupt", breakStore: func(t *testing.T, home string) {
			path := filepath.Join(home, ".ao", remotesFileName)
			if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unreadable", breakStore: func(t *testing.T, home string) {
			path := filepath.Join(home, ".ao", "remotes-directory")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(remotesFileEnv, path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := prepareAllHostsTest(t, "local-1")
			tc.breakStore(t, home)
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"session", "ls", "--all-hosts", "--include-terminated", "--json")
			if err != nil {
				t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
			}
			var got sessionListOutput
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Data) != 1 || got.Data[0].HostID != "h_local" {
				t.Fatalf("data = %#v, want healthy local result", got.Data)
			}
			if len(got.Meta.Hosts) != 2 || got.Meta.Hosts[1].HostLabel != "saved hosts" || got.Meta.Hosts[1].Error == "" {
				t.Fatalf("hosts = %#v, want structured discovery failure", got.Meta.Hosts)
			}
		})
	}
}

func TestSessionListAllHostsJSONUsesEmptyArrayForEmptyFleet(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "")
	writeRemotes(t, home, 0o600)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if got := string(raw["data"]); got != "[]" {
		t.Fatalf("data JSON = %s, want []", got)
	}
}

func TestSessionListAllHostsBoundsRemoteFanout(t *testing.T) {
	home, _ := prepareAllHostsTest(t, "")
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	started := make(chan struct{}, 6)
	release := make(chan struct{})
	go func() {
		<-started
		time.Sleep(250 * time.Millisecond)
		close(release)
	}()

	entries := make([]remoteEntry, 0, 6)
	for i := 0; i < 6; i++ {
		hostID := "h_" + string(rune('a'+i))
		token := "aoc_" + string(rune('a'+i))
		firstIdentity := &atomic.Bool{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/identity" {
				if firstIdentity.CompareAndSwap(false, true) {
					current := inFlight.Add(1)
					for {
						observed := maxInFlight.Load()
						if current <= observed || maxInFlight.CompareAndSwap(observed, current) {
							break
						}
					}
					started <- struct{}{}
					<-release
					inFlight.Add(-1)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"hostId":"`+hostID+`","apiVersion":1}`)
				return
			}
			if r.URL.Path == "/api/v1/sessions" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"sessions":[]}`)
				return
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(srv.Close)
		entries = append(entries, remoteEntry{Label: hostID, URL: srv.URL, HostID: hostID, CoordinationToken: token})
	}
	writeRemotes(t, home, 0o600, entries...)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "ls", "--all-hosts", "--all", "--include-terminated", "--json")
	if err != nil {
		t.Fatalf("session ls --all-hosts: %v\nstderr=%s", err, errOut)
	}
	if got := maxInFlight.Load(); got < 2 || got > 4 {
		t.Fatalf("remote fanout concurrency = %d, want 2..4", got)
	}
}

func TestSessionListAllHostsRejectsSingleRemoteTargetBeforeResolution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		envURL string
		args   []string
	}{
		{name: "missing saved host", args: []string{"session", "ls", "--all-hosts", "--host", "missing"}},
		{name: "raw URL without credential", args: []string{"session", "ls", "--all-hosts", "--url", "http://127.0.0.1:1"}},
		{name: "AO_URL without credential", envURL: "http://127.0.0.1:1", args: []string{"session", "ls", "--all-hosts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aoHome(t)
			t.Setenv("AO_URL", tc.envURL)
			_, _, err := executeCLI(t, Deps{}, tc.args...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--all-hosts cannot be combined") {
				t.Fatalf("err=%v exit=%d, want target-conflict usage error", err, ExitCode(err))
			}
		})
	}
}
