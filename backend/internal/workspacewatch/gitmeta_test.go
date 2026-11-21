package workspacewatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// newCommittedRepo creates a repository with files spread over dirs
// directories and commits them.
func newCommittedRepo(t *testing.T, dirs, filesPerDir int) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "ao@example.com")
	runGit(t, root, "config", "user.name", "AO Tests")
	for d := range dirs {
		dir := filepath.Join(root, fmt.Sprintf("dir-%02d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create dir: %v", err)
		}
		for f := range filesPerDir {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%02d.txt", f)), []byte("x\n"), 0o600); err != nil {
				t.Fatalf("create file: %v", err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "initial")
	return root
}

// rewriteIndex replaces root's index the way Git does: write index.lock, then
// rename it over index.
func rewriteIndex(t *testing.T, root string) {
	t.Helper()
	gitDir, err := resolveGitDir(context.Background(), root)
	if err != nil {
		t.Fatalf("resolve git dir: %v", err)
	}
	index := filepath.Join(gitDir, "index")
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	lock := index + ".lock"
	if err := os.WriteFile(lock, raw, 0o600); err != nil {
		t.Fatalf("write index.lock: %v", err)
	}
	if err := os.Rename(lock, index); err != nil {
		t.Fatalf("rename index.lock: %v", err)
	}
}

func expectNoChange(t *testing.T, changes <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-changes:
		t.Fatal(why)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestWatchGitMetadataDescriptorsStayFlatAcrossManyWorktrees is the regression
// test for a daemon that ran out of descriptors: the collision radar watched
// every file of every worktree in a project (about 18,000 descriptors per
// worktree on macOS) and a 25-worktree project could never fit. The metadata
// watch must cost a few descriptors per worktree however many files it holds,
// and must not accumulate descriptors as Git rewrites the index again and again.
func TestWatchGitMetadataDescriptorsStayFlatAcrossManyWorktrees(t *testing.T) {
	const (
		worktrees   = 12
		filesPerDir = 20
		dirs        = 20
		cycles      = 25
	)
	main := newCommittedRepo(t, dirs, filesPerDir)
	roots := []string{main}
	parent := t.TempDir()
	for i := range worktrees {
		path := filepath.Join(parent, fmt.Sprintf("wt-%02d", i))
		runGit(t, main, "worktree", "add", "-q", "-b", fmt.Sprintf("wt-%02d", i), path)
		roots = append(roots, path)
	}

	baseline, ok := openDescriptors(t)
	if !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}
	ctx, cancel := context.WithCancel(context.Background())
	subscription, err := WatchGitMetadata(ctx, roots...)
	if err != nil {
		cancel()
		t.Fatalf("WatchGitMetadata: %v", err)
	}
	if len(subscription.RootErrors) != 0 {
		cancel()
		t.Fatalf("RootErrors = %v, want none", subscription.RootErrors)
	}
	watching, _ := openDescriptors(t)
	files := len(roots) * dirs * filesPerDir
	if grown, limit := watching-baseline, len(roots)*25; grown > limit {
		cancel()
		t.Fatalf("metadata watch of %d worktrees (%d files) holds %d descriptors, want at most %d", len(roots), files, grown, limit)
	}

	for cycle := range cycles {
		for _, root := range roots {
			rewriteIndex(t, root)
		}
		select {
		case <-subscription.Changes:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatalf("cycle %d: index rewrite was not reported", cycle)
		}
	}
	// kqueue swaps the replaced index's descriptor asynchronously and briefly
	// holds index.lock while Git renames it, so allow a few in flight. A leak
	// would grow with every rewrite: 13 worktrees x 25 cycles.
	const inFlight = 10
	deadline := time.Now().Add(5 * time.Second)
	after, _ := openDescriptors(t)
	for after > watching+inFlight && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		after, _ = openDescriptors(t)
	}
	if after > watching+inFlight {
		cancel()
		t.Fatalf("descriptors after %d index rewrites in %d worktrees = %d, want about %d", cycles, len(roots), after, watching)
	}

	cancel()
	for range subscription.Changes { //nolint:revive // drain until the watcher closes
	}
	deadline = time.Now().Add(5 * time.Second)
	for after > baseline && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		after, _ = openDescriptors(t)
	}
	if after > baseline {
		t.Fatalf("descriptors after cancel = %d, want at most the %d baseline", after, baseline)
	}
}

func TestWatchGitMetadataReportsHeadAndIndexChangesOnly(t *testing.T) {
	main := newCommittedRepo(t, 1, 1)
	worktree := filepath.Join(t.TempDir(), "linked")
	runGit(t, main, "worktree", "add", "-q", "-b", "linked", worktree)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription, err := WatchGitMetadata(ctx, worktree)
	if err != nil {
		t.Fatalf("WatchGitMetadata: %v", err)
	}

	if err := os.WriteFile(filepath.Join(worktree, "notes.md"), []byte("draft\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	expectNoChange(t, subscription.Changes, "an edit that touches neither HEAD nor the index was reported")
	runGit(t, main, "status", "--porcelain")
	expectNoChange(t, subscription.Changes, "the main checkout's git status was reported for a linked worktree")

	runGit(t, worktree, "add", "notes.md")
	waitForChange(t, subscription.Changes)
	drainChanges(subscription.Changes)

	runGit(t, worktree, "commit", "-q", "-m", "notes")
	waitForChange(t, subscription.Changes)
	drainChanges(subscription.Changes)

	runGit(t, worktree, "checkout", "-q", "-b", "next")
	waitForChange(t, subscription.Changes)
}

func TestWatchGitMetadataKeepsGoodRootsWhenOneFails(t *testing.T) {
	good := newCommittedRepo(t, 1, 1)
	notGit := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription, err := WatchGitMetadata(ctx, good, notGit)
	if err != nil {
		t.Fatalf("WatchGitMetadata with one good root: %v", err)
	}
	if len(subscription.RootErrors) != 1 {
		t.Fatalf("RootErrors = %v, want one for the non-Git root", subscription.RootErrors)
	}
	select {
	case <-subscription.Degraded:
	default:
		t.Fatal("partial coverage did not signal Degraded")
	}
	rewriteIndex(t, good)
	waitForChange(t, subscription.Changes)
}

func TestWatchGitMetadataFailsWhenNoRootCanBeWatched(t *testing.T) {
	if _, err := WatchGitMetadata(context.Background(), t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("WatchGitMetadata of two non-Git roots succeeded")
	}
}

func TestWatchWithStatusKeepsGoodRootsWhenOneFails(t *testing.T) {
	good := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription, err := WatchWithStatus(ctx, good, missing)
	if err != nil {
		t.Fatalf("WatchWithStatus with one good root: %v", err)
	}
	if len(subscription.RootErrors) != 1 || !errors.Is(subscription.RootErrors[0], fs.ErrNotExist) {
		t.Fatalf("RootErrors = %v, want the missing root's not-exist error", subscription.RootErrors)
	}
	select {
	case <-subscription.Degraded:
	default:
		t.Fatal("partial coverage did not signal Degraded")
	}
	if err := os.WriteFile(filepath.Join(good, "plan.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	waitForChange(t, subscription.Changes)
}

func TestWatchWithStatusFailsWhenNoRootCanBeWatched(t *testing.T) {
	parent := t.TempDir()
	if _, err := WatchWithStatus(context.Background(), filepath.Join(parent, "a"), filepath.Join(parent, "b")); err == nil {
		t.Fatal("WatchWithStatus of two missing roots succeeded")
	}
}

// Watch callers cannot see partial coverage, so Watch keeps failing when any
// root cannot be watched.
func TestWatchFailsWhenAnyRootFails(t *testing.T) {
	if _, err := Watch(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("Watch with a missing root succeeded")
	}
}

func TestDescriptorsExhausted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"EMFILE wrapped", fmt.Errorf("create workspace watcher: %w", syscall.EMFILE), true},
		{"ENFILE in a path error", &fs.PathError{Op: "open", Path: "/x", Err: syscall.ENFILE}, true},
		{"EMFILE joined", errors.Join(errors.New("other"), fmt.Errorf("watch: %w", syscall.EMFILE)), true},
		{"not exist", fs.ErrNotExist, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DescriptorsExhausted(tc.err); got != tc.want {
				t.Fatalf("DescriptorsExhausted(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// Cancelling right after Git replaced the index races fsnotify re-opening the
// new index file against Close. A watch that is rebuilt often (the collision
// radar rebuilds on every topology change) must not leak that descriptor.
func TestWatchGitMetadataReleasesReopenedIndexOnCancel(t *testing.T) {
	root := newCommittedRepo(t, 2, 2)
	baseline, ok := openDescriptors(t)
	if !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}
	for round := range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		subscription, err := WatchGitMetadata(ctx, root)
		if err != nil {
			cancel()
			t.Fatalf("round %d: WatchGitMetadata: %v", round, err)
		}
		rewriteIndex(t, root)
		select {
		case <-subscription.Changes:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatalf("round %d: index rewrite was not reported", round)
		}
		cancel()
		for range subscription.Changes { //nolint:revive // drain until the watcher closes
		}
	}
	if after, _ := openDescriptors(t); after > baseline {
		t.Fatalf("descriptors after 20 cancelled watches = %d, want at most the %d baseline", after, baseline)
	}
}
