//go:build linux

package sessionartifacts

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
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

func TestFileHandleGenerationIgnoresTransientMountID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("# Plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity := quickIdentity(info)
	handle := unix.NewFileHandle(1, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	mountID := 7
	original := nameToHandleAt
	nameToHandleAt = func(int, string, int) (unix.FileHandle, int, error) {
		return handle, mountID, nil
	}
	t.Cleanup(func() { nameToHandleAt = original })

	before, ok := fileHandleGeneration(path, identity.Ino)
	if !ok {
		t.Fatal("stubbed file handle was not accepted")
	}
	mountID = 99
	after, ok := fileHandleGeneration(path, identity.Ino)
	if !ok || after != before {
		t.Fatalf("generation after mount id change = %d, %v; want %d", after, ok, before)
	}
}
