package workspacesnapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCaptureRequiresEveryRepositoryToBeClean(t *testing.T) {
	repo := snapshotRepo(t)
	want, err := Capture(context.Background(), repo, nil)
	if err != nil || want == "" {
		t.Fatalf("clean snapshot = %q, err=%v", want, err)
	}

	for _, dirty := range []struct {
		name string
		path string
		add  bool
	}{
		{name: "unstaged", path: "tracked.txt"},
		{name: "staged", path: "tracked.txt", add: true},
		{name: "untracked", path: "new.txt"},
	} {
		t.Run(dirty.name, func(t *testing.T) {
			repoCopy := snapshotRepo(t)
			if err := os.WriteFile(filepath.Join(repoCopy, dirty.path), []byte(dirty.name), 0o600); err != nil {
				t.Fatal(err)
			}
			if dirty.add {
				snapshotGit(t, repoCopy, "add", dirty.path)
			}
			if got, err := Capture(context.Background(), repoCopy, nil); err == nil || got != "" {
				t.Fatalf("dirty snapshot = %q, err=%v; want rejected", got, err)
			}
		})
	}
}

func TestCaptureBindsEveryWorkspaceRepository(t *testing.T) {
	root := snapshotRepo(t)
	child := snapshotRepo(t)
	rows := []domain.SessionWorktreeRecord{
		{RepoName: domain.RootWorkspaceRepoName, WorktreePath: root},
		{RepoName: "child", WorktreePath: child},
	}
	before, err := Capture(context.Background(), root, rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotGit(t, child, "add", "child.txt")
	snapshotGit(t, child, "commit", "-m", "child")
	after, err := Capture(context.Background(), root, rows)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("snapshot did not change after child commit: %q", before)
	}
}

func snapshotRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	snapshotGit(t, repo, "init")
	snapshotGit(t, repo, "config", "user.name", "AO Test")
	snapshotGit(t, repo, "config", "user.email", "ao@example.test")
	snapshotGit(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotGit(t, repo, "add", ".")
	snapshotGit(t, repo, "commit", "-m", "base")
	return repo
}

func snapshotGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...) //nolint:gosec // Test owns every argument.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
