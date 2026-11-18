//go:build windows

package sessionartifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsFileIdentitySurvivesRename(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "plan.md")
	to := filepath.Join(dir, "project-plan.md")
	if err := os.WriteFile(from, []byte("# Plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(from)
	if err != nil {
		t.Fatal(err)
	}
	before := fullIdentity(from, beforeInfo)
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Lstat(to)
	if err != nil {
		t.Fatal(err)
	}
	if after := fullIdentity(to, afterInfo); !before.SameFile(after) {
		t.Fatalf("renamed file identity changed: before=%+v after=%+v", before, after)
	}
}
