//go:build linux

package sessionartifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileHandleGenerationSurvivesRenameAndEdit(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "plan.md")
	to := filepath.Join(dir, "project-plan.md")
	if err := os.WriteFile(from, []byte("# Plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(from)
	if err != nil {
		t.Fatal(err)
	}
	identity := quickIdentity(info)
	before, ok := fileHandleGeneration(from, identity.Ino)
	if !ok {
		t.Skip("file system does not expose stable file handles")
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("# Edited plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, ok := fileHandleGeneration(to, identity.Ino)
	if !ok || after != before {
		t.Fatalf("file handle generation after rename/edit = %d, %v; want %d", after, ok, before)
	}
}
