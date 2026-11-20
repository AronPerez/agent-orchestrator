package mobilebridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceHostIDKeepsTheRestOfTheIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile", "identity.json")
	issued, err := EnsureIdentityFor(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadHostID(path); err != nil || got != issued.HostID {
		t.Fatalf("ReadHostID = %q, %v", got, err)
	}
	if err := ReplaceHostID(path, "h_new"); err != nil {
		t.Fatal(err)
	}
	again, err := EnsureIdentityFor(path, nil)
	if err != nil || again.HostID != "h_new" || again.Fingerprint != issued.Fingerprint {
		t.Fatalf("after replace = %+v, %v", again, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("left %d files behind", len(entries))
	}
}
