package decisionsync_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

func TestPeerSecretsFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes; Windows relies on the per-user data directory ACL, as remotes.json does")
	}
	dir := t.TempDir()
	path := decisionsync.PeerSecretsPath(dir)
	secrets, err := decisionsync.LoadPeerSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(decisionsync.StoredPeer{HostID: "h_mini", URL: "http://10.0.0.2:3011", Label: "mac-mini", DeviceID: "dev_1", Token: "aoc_secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("peers.json mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("decision-sync dir mode = %v, %v; want 0700", dirInfo.Mode().Perm(), err)
	}
	if path != filepath.Join(dir, "decision-sync", "peers.json") {
		t.Fatalf("path = %s", path)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if e.Name() != "peers.json" {
			t.Fatalf("left a stray file %s after an atomic write", e.Name())
		}
	}
	reloaded, err := decisionsync.LoadPeerSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.Get("h_mini"); !ok || got.Token != "aoc_secret" || got.DeviceID != "dev_1" {
		t.Fatalf("reloaded = %+v, %v", got, ok)
	}
	if _, ok, err := reloaded.Delete("h_mini"); !ok || err != nil {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if again, _ := decisionsync.LoadPeerSecrets(path); len(again.List()) != 0 {
		t.Fatal("delete did not persist")
	}
}

func TestPeerSecretsSurviveDamage(t *testing.T) {
	dir := t.TempDir()
	path := decisionsync.PeerSecretsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets, err := decisionsync.LoadPeerSecrets(path)
	if err == nil || !strings.Contains(err.Error(), "moved aside") {
		t.Fatalf("damaged load err = %v, want a moved-aside report", err)
	}
	if secrets == nil || len(secrets.List()) != 0 {
		t.Fatal("damaged file did not start an empty registry")
	}
	if raw, err := os.ReadFile(path + ".bad"); err != nil || string(raw) != "{not json" {
		t.Fatalf("damaged copy = %q, %v", raw, err)
	}
	if err := secrets.Put(decisionsync.StoredPeer{HostID: "h_mini", Token: "aoc_x"}); err != nil {
		t.Fatalf("write after damage: %v", err)
	}
}
