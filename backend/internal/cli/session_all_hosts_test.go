package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/identity" {
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

func TestSessionListAllHostsRejectsSingleRemoteTarget(t *testing.T) {
	aoHome(t)
	t.Setenv("AO_TOKEN", "explicit-token")
	_, _, err := executeCLI(t, Deps{}, "session", "ls", "--all-hosts", "--url", "https://remote.example")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--all-hosts cannot be combined") {
		t.Fatalf("err=%v exit=%d, want target-conflict usage error", err, ExitCode(err))
	}
}

func TestAllHostSessionJSONExposesQualifiedParentEdges(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	results := []allHostSessionListResult{
		{
			HostID: "h_local", HostLabel: "this machine", Local: true,
			Snapshot: sessionListSnapshot{Sessions: []sessionDTO{{ID: "local-child", ParentSessionID: "local-parent"}}},
		},
		{
			HostID: "h_remote", HostLabel: "build",
			Snapshot: sessionListSnapshot{Sessions: []sessionDTO{{ID: "remote-child", ParentHostID: "h_local", ParentSessionID: "local-parent"}}},
		},
	}
	if err := writeAllHostSessionListJSON(cmd, results); err != nil {
		t.Fatal(err)
	}
	var got sessionListOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]sessionListEntry, len(got.Data))
	for _, entry := range got.Data {
		byID[entry.ID] = entry
	}
	if child := byID["local-child"]; child.ParentHostID != "h_local" || child.ParentSessionID != "local-parent" {
		t.Fatalf("local parent edge = %#v, want h_local/local-parent", child)
	}
	if child := byID["remote-child"]; child.ParentHostID != "h_local" || child.ParentSessionID != "local-parent" {
		t.Fatalf("remote parent edge = %#v, want h_local/local-parent", child)
	}
}
