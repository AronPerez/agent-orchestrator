package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// peerDaemon fakes the local daemon's peer routes and records what it got.
type peerDaemon struct {
	linkBodies []map[string]any
	synced     []string
	deleted    []string
}

func (d *peerDaemon) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/internal/"):
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/decision-peers":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			d.linkBodies = append(d.linkBodies, body)
			_, _ = io.WriteString(w, `{"peer":{"hostId":"h_mini","label":"mac-mini","direction":"outbound","state":"linked_behind","inSync":false,"clockAheadMs":0},"warnings":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/decision-peers/sync":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			d.synced = append(d.synced, body["hostId"])
			_, _ = io.WriteString(w, `{"results":[{"hostId":"h_mini","label":"mac-mini","pulled":12,"pushed":3,"inSync":true},{"hostId":"h_vm","label":"vm-2","pulled":0,"pushed":0,"inSync":false,"errorCode":"unreachable"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/decision-peers":
			_, _ = io.WriteString(w, `{"peers":[
{"hostId":"h_mini","label":"mac-mini","direction":"outbound","state":"linked_in_sync","lastSuccessAt":"2026-11-19T11:58:00Z","inSync":true,"clockAheadMs":0},
{"hostId":"h_phone_box","label":"","direction":"inbound","state":"inbound","lastSuccessAt":"2026-11-19T10:00:00Z","inSync":false,"clockAheadMs":0},
{"hostId":"h_vm","label":"vm-2","direction":"outbound","state":"revoked","inSync":false,"clockAheadMs":0}]}`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/decision-peers/"):
			d.deleted = append(d.deleted, strings.TrimPrefix(r.URL.Path, "/api/v1/decision-peers/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func writeSavedHosts(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remotes.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_REMOTES_FILE", path)
}

func TestDecisionLinkAddRefusedFromSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	t.Setenv("AO_TOKEN", "pw")
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Header.Get("X-AO-Caller-Session") != "w-1" {
			t.Errorf("caller header = %q", r.Header.Get("X-AO-Caller-Session"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"kind":"forbidden","code":"DECISION_PERSON_ONLY","message":"Only the person can link machines or sync decisions. A session cannot.","requestId":"r"}`)
	}))
	_, _, err := executeCLI(t, deps, "decision", "link", "add", "http://mini:3011")
	if err == nil || !strings.Contains(err.Error(), "Only the person can link machines") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecisionLinkAddReadsSavedHost(t *testing.T) {
	writeSavedHosts(t, `{"remotes":[{"label":"mac-mini","url":"http://mini.local:3011","password":"saved-pw","hostId":"h_mini","coordinationToken":"aoc_coord"}]}`)
	d := &peerDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	out, _, err := executeCLI(t, deps, "decision", "link", "add", "--host", "mac-mini")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.linkBodies) != 1 {
		t.Fatalf("link bodies = %v", d.linkBodies)
	}
	got := d.linkBodies[0]
	if got["url"] != "http://mini.local:3011" || got["expectedHostId"] != "h_mini" || got["credential"] != "saved-pw" || got["label"] != "mac-mini" {
		t.Fatalf("link body = %v", got)
	}
	if len(d.synced) != 1 || d.synced[0] != "h_mini" {
		t.Fatalf("first sync = %v", d.synced)
	}
	if !strings.Contains(out, "Linked mac-mini (h_mini).") || !strings.Contains(out, "First sync: pulled 12, pushed 3, in sync") {
		t.Fatalf("out = %q", out)
	}
}

func TestDecisionLinkAddSealedPasswordMessage(t *testing.T) {
	writeSavedHosts(t, `{"remotes":[{"label":"mac-mini","url":"http://mini.local:3011","password":"","sealedPassword":"v10:abc","hostId":"h_mini"}]}`)
	d := &peerDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	_, _, err := executeCLI(t, deps, "decision", "link", "add", "--host", "h_mini")
	if err == nil || !strings.Contains(err.Error(), "stored by the desktop app. Turn on Sync decisions for it in Settings › Remote hosts") {
		t.Fatalf("err = %v", err)
	}
	if len(d.linkBodies) != 0 {
		t.Fatal("a sealed host was sent to the daemon")
	}
}

func TestDecisionLinkLsText(t *testing.T) {
	d := &peerDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	deps.Now = func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }
	out, _, err := executeCLI(t, deps, "decision", "link", "ls")
	if err != nil {
		t.Fatal(err)
	}
	want := "mac-mini\tlinked\tin sync\tlast synced 2m ago\n" +
		"h_phone_box\tlinks to this computer\tsyncs when it connects\tlast synced 2h ago\n" +
		"vm-2\tlinked\tno longer accepts this computer\tnever synced\n"
	if out != want {
		t.Fatalf("ls =\n%s\nwant\n%s", out, want)
	}
}

func TestDecisionSyncPrintsCounts(t *testing.T) {
	d := &peerDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	out, _, err := executeCLI(t, deps, "decision", "sync", "mac-mini")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.synced) != 1 || d.synced[0] != "h_mini" {
		t.Fatalf("synced = %v", d.synced)
	}
	if !strings.Contains(out, "mac-mini: pulled 12, pushed 3, in sync\n") || !strings.Contains(out, "vm-2: not answering (unreachable)\n") {
		t.Fatalf("out = %q", out)
	}
	if _, _, err := executeCLI(t, deps, "decision", "link", "rm", "vm-2"); err != nil || len(d.deleted) != 1 || d.deleted[0] != "h_vm" {
		t.Fatalf("rm = %v, deleted %v", err, d.deleted)
	}
}

func TestDecisionLinkNeverPrintsToken(t *testing.T) {
	writeSavedHosts(t, `{"remotes":[{"label":"mac-mini","url":"http://mini.local:3011","password":"saved-pw-77","hostId":"h_mini","coordinationToken":"aoc_coord_99"}]}`)
	d := &peerDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	out, errOut, err := executeCLI(t, deps, "decision", "link", "add", "--host", "mac-mini")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"saved-pw-77", "aoc_coord_99", "aoc_"} {
		if strings.Contains(out+errOut, secret) {
			t.Fatalf("output shows %q: %s %s", secret, out, errOut)
		}
	}
	lsOut, _, _ := executeCLI(t, deps, "decision", "link", "ls", "--json")
	if strings.Contains(lsOut, "token") || strings.Contains(lsOut, "aoc_") {
		t.Fatalf("ls --json shows a token: %s", lsOut)
	}
}

func TestDecisionLinkAddReadsPasswordFromStdin(t *testing.T) {
	d := &peerDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	deps.In = strings.NewReader("stdin-pw\n")
	if _, _, err := executeCLI(t, deps, "decision", "link", "add", "http://mini:3011", "--password-stdin", "--label", "mini"); err != nil {
		t.Fatal(err)
	}
	if got := d.linkBodies[0]; got["credential"] != "stdin-pw" || got["url"] != "http://mini:3011" || got["label"] != "mini" {
		t.Fatalf("link body = %v", got)
	}
	if _, _, err := executeCLI(t, deps, "decision", "link", "add", "http://mini:3011"); err == nil || !strings.Contains(err.Error(), "--password-stdin or AO_TOKEN") {
		t.Fatalf("missing password err = %v", err)
	}
}
