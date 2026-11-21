package session

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/workspacewatch"
)

// startOverlapRadar runs the daemon-owned radar until the returned stop runs.
func startOverlapRadar(t *testing.T, svc *Service) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

func waitForOverlapNudges(t *testing.T, nudger *recordingOverlapNudger, count int, why string) {
	t.Helper()
	for range count {
		select {
		case <-nudger.callCh:
		case <-time.After(5 * time.Second):
			t.Fatal(why)
		}
	}
}

func waitForOverlapScan(t *testing.T, store *memoryOverlapNoticeStore) {
	t.Helper()
	select {
	case <-store.listNoticeCh:
	case <-time.After(5 * time.Second):
		t.Fatal("radar did not finish its initial scan")
	}
}

// An edit that is not staged touches neither HEAD nor the index, so only the
// poll can find it.
func TestRunProjectOverlapNoticeRadarFindsUnstagedOverlapByPoll(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapRadarPollInterval = 50 * time.Millisecond
	stop := startOverlapRadar(t, svc)

	waitForOverlapScan(t, store)
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	waitForOverlapNudges(t, nudger, 2, "the poll did not find an unstaged overlap")
	stop()
	if len(store.notices) != 2 {
		t.Fatalf("notices = %+v, want both directions", store.notices)
	}
}

// A watch that fails for lack of file descriptors must not be retried every
// retry delay: each attempt takes the daemon's last free descriptors, and the
// lock and database opens that land in that window fail. The poll keeps
// finding overlaps while the watch is backed off.
func TestRunProjectOverlapNoticeRadarBacksOffAfterDescriptorExhaustion(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	svc.overlapRadarPollInterval = 50 * time.Millisecond
	svc.overlapRadarBackoffBase = 300 * time.Millisecond
	svc.overlapRadarBackoffMax = 600 * time.Millisecond
	attempts := make(chan time.Time, 8)
	svc.overlapWatch = func(context.Context, ...string) (workspacewatch.Subscription, error) {
		attempts <- time.Now()
		return workspacewatch.Subscription{}, fmt.Errorf("create git metadata watcher: %w", syscall.EMFILE)
	}
	stop := startOverlapRadar(t, svc)

	waitForOverlapScan(t, store)
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	waitForOverlapNudges(t, nudger, 2, "the poll did not find an overlap while the watch was backed off")

	var times []time.Time
	for len(times) < 3 {
		select {
		case at := <-attempts:
			times = append(times, at)
		case <-time.After(5 * time.Second):
			t.Fatalf("watch attempts = %d, want 3", len(times))
		}
	}
	stop()
	// Without backoff the radar retried every retry delay (10ms here).
	if gap := times[1].Sub(times[0]); gap < 250*time.Millisecond {
		t.Fatalf("second attempt came %v after the first, want the 300ms backoff", gap)
	}
	if gap := times[2].Sub(times[1]); gap < 550*time.Millisecond {
		t.Fatalf("third attempt came %v after the second, want the doubled 600ms backoff", gap)
	}
}

// When some roots cannot be watched for a reason other than running out of
// descriptors, the roots that were watched keep reporting changes until the
// backed-off rebuild.
func TestRunProjectOverlapNoticeRadarKeepsWatchedRootsOnPartialFailure(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	// Other failures are rebuilt after the retry delay; keep it out of reach so
	// only the kept watch can deliver.
	svc.overlapNoticeRetryDelay = time.Minute
	var (
		mu      sync.Mutex
		watches int
	)
	svc.overlapWatch = func(ctx context.Context, paths ...string) (workspacewatch.Subscription, error) {
		mu.Lock()
		watches++
		mu.Unlock()
		subscription, err := workspacewatch.WatchGitMetadata(ctx, paths...)
		subscription.RootErrors = append(subscription.RootErrors, fmt.Errorf("watch git metadata for another worktree: %w", fs.ErrPermission))
		return subscription, err
	}
	stop := startOverlapRadar(t, svc)

	waitForOverlapScan(t, store)
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	runGit(t, second, "add", "shared.go")
	// The poll runs every 30s by default, so only the kept watch can deliver.
	waitForOverlapNudges(t, nudger, 2, "the watched roots stopped reporting after a partial watch failure")
	stop()
	mu.Lock()
	defer mu.Unlock()
	if watches != 1 {
		t.Fatalf("watch attempts = %d, want 1 within the retry delay", watches)
	}
}

// Changes from the roots that are watched must not postpone rewatching a root
// that failed: with one root failing and another staging changes faster than
// the retry delay, the failed root was never rewatched and stayed poll-only.
func TestRunProjectOverlapNoticeRadarRewatchesFailedRootWhileOthersKeepChanging(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: &recordingOverlapNudger{}})
	svc.overlapNoticeRetryDelay = 300 * time.Millisecond
	rewatched := make(chan struct{})
	var (
		mu      sync.Mutex
		watches int
	)
	svc.overlapWatch = func(ctx context.Context, paths ...string) (workspacewatch.Subscription, error) {
		mu.Lock()
		watches++
		attempt := watches
		mu.Unlock()
		if attempt > 1 {
			close(rewatched)
			return workspacewatch.WatchGitMetadata(ctx, paths...)
		}
		// Watch only the first worktree; the second "failed".
		subscription, err := workspacewatch.WatchGitMetadata(ctx, first)
		subscription.RootErrors = append(subscription.RootErrors, fmt.Errorf("watch git metadata for %s: %w", second, fs.ErrPermission))
		return subscription, err
	}
	startOverlapRadar(t, svc)
	waitForOverlapScan(t, store)

	// Stage a change in the watched worktree every 50ms.
	staging := make(chan struct{})
	stagingDone := make(chan struct{})
	go func() {
		defer close(stagingDone)
		for i := 0; ; i++ {
			select {
			case <-staging:
				return
			case <-time.After(50 * time.Millisecond):
			}
			name := fmt.Sprintf("churn-%03d.md", i)
			if os.WriteFile(filepath.Join(first, name), []byte("x\n"), 0o644) == nil {
				_ = exec.Command("git", "-C", first, "add", name).Run()
			}
		}
	}()
	defer func() {
		close(staging)
		<-stagingDone
	}()

	select {
	case <-rewatched:
	case <-time.After(3 * time.Second):
		t.Fatal("the failed root was not rewatched while the watched root kept changing")
	}
}

// A partial watch reported with descriptor exhaustion is released at once,
// not held through the backoff; the poll keeps finding overlaps.
func TestRunProjectOverlapNoticeRadarReleasesPartialWatchWhenDescriptorsRunOut(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	svc.overlapRadarPollInterval = 50 * time.Millisecond
	svc.overlapRadarBackoffBase = time.Minute
	released := make(chan struct{})
	var (
		mu      sync.Mutex
		watches int
	)
	svc.overlapWatch = func(ctx context.Context, paths ...string) (workspacewatch.Subscription, error) {
		mu.Lock()
		watches++
		mu.Unlock()
		subscription, err := workspacewatch.WatchGitMetadata(ctx, paths...)
		subscription.RootErrors = append(subscription.RootErrors, fmt.Errorf("watch git metadata for another worktree: %w", syscall.EMFILE))
		go func() {
			<-ctx.Done()
			close(released)
		}()
		return subscription, err
	}
	stop := startOverlapRadar(t, svc)

	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("the radar held a partial watch after descriptors ran out")
	}
	waitForOverlapScan(t, store)
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	waitForOverlapNudges(t, nudger, 2, "the poll did not find an overlap after the partial watch was released")
	stop()
	mu.Lock()
	defer mu.Unlock()
	if watches != 1 {
		t.Fatalf("watch attempts = %d, want 1 within the backoff", watches)
	}
}

// openDescriptorCount counts this process's open file descriptors.
func openDescriptorCount(t *testing.T) (int, bool) {
	t.Helper()
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return 0, false
	}
	defer func() { _ = dir.Close() }()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return 0, false
	}
	return len(names), true
}

// settledDescriptorCount is the smallest of a few samples, so the pipes of a
// Git command that happens to be running do not count.
func settledDescriptorCount(t *testing.T) int {
	t.Helper()
	least := -1
	for range 5 {
		if count, _ := openDescriptorCount(t); least < 0 || count < least {
			least = count
		}
		time.Sleep(20 * time.Millisecond)
	}
	return least
}

// TestRunProjectOverlapNoticeRadarDescriptorsStayFlatAcrossManyReconciles is
// the regression test for a daemon that ran out of file descriptors: the radar
// held one descriptor per file of every worktree in a project, so a project of
// 25 worktrees of 18,000 files each could never fit, and every retry took the
// rest. The radar must hold a few descriptors per worktree, independent of the
// files in it, and the count must not grow across reconcile cycles.
func TestRunProjectOverlapNoticeRadarDescriptorsStayFlatAcrossManyReconciles(t *testing.T) {
	const (
		worktrees = 8
		files     = 300
		cycles    = 25
	)
	if _, ok := openDescriptorCount(t); !ok {
		t.Skip("descriptor counting is unavailable on this platform")
	}
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	repos := make([]string, 0, worktrees)
	for i := range worktrees {
		repo := newWorkspaceRepo(t)
		for f := range files {
			writeWorkspaceFile(t, repo, fmt.Sprintf("pkg/dir-%02d/file-%03d.go", f%15, f), "package dir\n")
		}
		runGit(t, repo, "add", ".")
		runGit(t, repo, "commit", "-q", "-m", "fill")
		id := domain.SessionID(fmt.Sprintf("demo-%02d", i))
		store.sessions[id] = overlapSession(id, repo)
		repos = append(repos, repo)
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: &recordingOverlapNudger{}})
	// Drive every reconcile through the metadata watch instead of the poll, so
	// each sample is taken while no Git command is running.
	svc.overlapRadarPollInterval = time.Hour
	baseline := settledDescriptorCount(t)
	stop := startOverlapRadar(t, svc)

	waitForReconciles := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for store.listCalls("demo") < want {
			if time.Now().After(deadline) {
				t.Fatalf("radar ran %d reconciles, want %d", store.listCalls("demo"), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	reconcileAfterStaging := func(cycle int) {
		t.Helper()
		before := store.listCalls("demo")
		repo := repos[cycle%worktrees]
		writeWorkspaceFile(t, repo, fmt.Sprintf("notes/cycle-%02d.md", cycle), "x\n")
		runGit(t, repo, "add", ".")
		waitForReconciles(before + 1)
	}
	// One staged change can raise more than one metadata edge, and a second
	// reconcile's Git commands would be counted. Sample once none has started
	// for a while.
	idleDescriptorCount := func() int {
		t.Helper()
		for {
			seen := store.listCalls("demo")
			time.Sleep(150 * time.Millisecond)
			if store.listCalls("demo") == seen {
				return settledDescriptorCount(t)
			}
		}
	}
	waitForReconciles(1)
	reconcileAfterStaging(0)
	early := idleDescriptorCount()
	if held, limit := early-baseline, worktrees*25; held > limit {
		t.Fatalf("radar over %d worktrees of %d files holds %d descriptors, want at most %d", worktrees, files, held, limit)
	}
	for cycle := 1; cycle <= cycles; cycle++ {
		reconcileAfterStaging(cycle)
	}
	late := idleDescriptorCount()
	if late > early+3 {
		t.Fatalf("descriptors grew from %d to %d across %d reconciles", early, late, store.listCalls("demo"))
	}
	stop()
	// The watch closes asynchronously after the radar returns.
	deadline := time.Now().Add(5 * time.Second)
	after := settledDescriptorCount(t)
	for after > baseline && time.Now().Before(deadline) {
		after = settledDescriptorCount(t)
	}
	if after > baseline {
		t.Fatalf("descriptors after the radar stopped = %d, want the %d baseline", after, baseline)
	}
}

func TestProjectOverlapRadarRebuildBackoff(t *testing.T) {
	svc := &Service{}
	for exhausted, want := range []time.Duration{0, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		if got := svc.projectOverlapRadarRebuildBackoff(exhausted); got != want {
			t.Fatalf("backoff after %d exhausted attempts = %v, want %v", exhausted, got, want)
		}
	}
}
