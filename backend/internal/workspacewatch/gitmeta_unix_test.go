//go:build unix

package workspacewatch

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// When descriptors run out partway through, the roots already watched must be
// released too: holding them leaves the rest of the daemon (SQLite, locks, the
// Git commands of the caller's poll) without descriptors through the backoff.
func TestWatchGitMetadataReleasesWatchedRootsWhenDescriptorsRunOut(t *testing.T) {
	main := newCommittedRepo(t, 1, 1)
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	runGit(t, main, "worktree", "add", "-q", "-b", "first", first)
	runGit(t, main, "worktree", "add", "-q", "-b", "second", second)

	baseline, ok := openDescriptors(t)
	if !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}
	// Measure what watching one worktree costs, watcher included.
	ctx, cancel := context.WithCancel(context.Background())
	single, err := WatchGitMetadata(ctx, first)
	if err != nil {
		cancel()
		t.Fatalf("watch one worktree: %v", err)
	}
	withOne, _ := openDescriptors(t)
	cancel()
	for range single.Changes { //nolint:revive // drain until the watcher closes
	}
	cost := withOne - baseline

	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Skipf("read descriptor limit: %v", err)
	}
	restore := limit
	// Room for the first worktree's watch, not the second's.
	limit.Cur = uint64(baseline + cost + 2)
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Skipf("lower descriptor limit: %v", err)
	}
	restored := false
	restoreLimit := func() {
		if !restored {
			restored = true
			if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &restore); err != nil {
				t.Errorf("restore descriptor limit: %v", err)
			}
		}
	}
	t.Cleanup(restoreLimit)

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	subscription, err := WatchGitMetadata(ctx, first, second)
	restoreLimit()
	if err == nil {
		t.Fatalf("WatchGitMetadata kept a partial watch (RootErrors %v) after descriptors ran out", subscription.RootErrors)
	}
	if !DescriptorsExhausted(err) || !strings.Contains(err.Error(), second) || strings.Contains(err.Error(), first+":") {
		t.Fatalf("error = %v, want descriptor exhaustion on the second worktree only", err)
	}
	if after, _ := openDescriptors(t); after > baseline {
		t.Fatalf("descriptors after the failed watch = %d, want the %d baseline", after, baseline)
	}
}
