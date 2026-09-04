package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newFSRig(t *testing.T, home string) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	(&FSController{Home: func() (string, error) { return home, nil }}).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func listDirs(t *testing.T, srv *httptest.Server, path string) (int, ListDirsResponse) {
	t.Helper()
	q := ""
	if path != "" {
		q = "?path=" + url.QueryEscape(path)
	}
	resp, err := http.Get(srv.URL + "/fs/dirs" + q)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body ListDirsResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, body
}

// padName zero-pads so the cap test's directory names sort deterministically.
func padName(i int) string { return fmt.Sprintf("%04d", i) }

func TestListDirsShowsDirectoriesOnlyWithGitDetection(t *testing.T) {
	home := t.TempDir()
	// A plain dir, a git repo (.git directory), a worktree checkout (.git FILE),
	// a dotdir that must be skipped, and a plain file that must not appear.
	for _, d := range []string{"plain", "repo/.git", "worktree", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Worktrees carry `.git` as a FILE ("gitdir: ..."), not a directory. Both count.
	if err := os.WriteFile(filepath.Join(home, "worktree", ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "loose.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	status, body := listDirs(t, newFSRig(t, home), home)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	got := map[string]bool{}
	for _, e := range body.Entries {
		got[e.Name] = e.GitRepo
		if e.Path != filepath.Join(home, e.Name) {
			t.Errorf("entry %q path = %q, want %q", e.Name, e.Path, filepath.Join(home, e.Name))
		}
	}
	want := map[string]bool{"plain": false, "repo": true, "worktree": true}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want exactly %v (no dotdirs, no files)", got, want)
	}
	for name, gitRepo := range want {
		if got[name] != gitRepo {
			t.Errorf("entry %q gitRepo = %v, want %v", name, got[name], gitRepo)
		}
	}
}

func TestListDirsDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, body := listDirs(t, newFSRig(t, home), "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body.Path != home {
		t.Errorf("path = %q, want home %q", body.Path, home)
	}
	if body.Parent != filepath.Dir(home) {
		t.Errorf("parent = %q, want %q", body.Parent, filepath.Dir(home))
	}
}

func TestListDirsRejectsRelativeAndMissingPaths(t *testing.T) {
	home := t.TempDir()
	srv := newFSRig(t, home)

	if status, _ := listDirs(t, srv, "relative/path"); status != http.StatusBadRequest {
		t.Errorf("relative path status = %d, want 400", status)
	}
	if status, _ := listDirs(t, srv, filepath.Join(home, "nope")); status != http.StatusNotFound {
		t.Errorf("missing path status = %d, want 404", status)
	}
	// A file is not browsable.
	f := filepath.Join(home, "f.txt")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _ := listDirs(t, srv, f); status != http.StatusBadRequest {
		t.Errorf("file path status = %d, want 400", status)
	}
}

func TestListDirsCapsEntries(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < maxDirEntries+10; i++ {
		if err := os.Mkdir(filepath.Join(home, "d"+padName(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	status, body := listDirs(t, newFSRig(t, home), home)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if len(body.Entries) != maxDirEntries || !body.Truncated {
		t.Errorf("entries = %d truncated = %v, want %d/true", len(body.Entries), body.Truncated, maxDirEntries)
	}
}
