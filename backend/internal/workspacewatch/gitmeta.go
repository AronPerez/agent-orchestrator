package workspacewatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// gitMetadataFiles are the per-worktree Git files whose changes WatchGitMetadata
// reports: HEAD moves on commit, checkout and reset; index changes on add,
// commit, stash and merge.
var gitMetadataFiles = []string{"HEAD", "index"}

// WatchGitMetadata reports changes to each root's Git HEAD and index, and
// nothing else. It watches each root's Git directory (for a linked worktree,
// .git/worktrees/<name>), so its cost is one descriptor per entry of that
// directory, about ten, however many files the worktree holds. A full-tree
// Watch costs one descriptor per worktree file on macOS, which a project with
// many large worktrees cannot afford. Edits that do not touch HEAD or the index
// are not reported; callers pair this with a bounded poll.
//
// All roots share one watcher. A root whose Git directory cannot be found or
// watched is reported in RootErrors and through Degraded while the others stay
// watched; WatchGitMetadata fails when no root could be watched, and when any
// root failed for lack of file descriptors, releasing every root it watched.
func WatchGitMetadata(ctx context.Context, roots ...string) (Subscription, error) {
	uniqueRoots, err := uniqueWatchRoots(roots)
	if err != nil {
		return Subscription{}, err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return Subscription{}, fmt.Errorf("create git metadata watcher: %w", err)
	}

	gitDirs := make(map[string]struct{}, len(uniqueRoots))
	targets := make(map[string]struct{}, len(uniqueRoots)*len(gitMetadataFiles))
	var rootErrors []error
	for _, root := range uniqueRoots {
		gitDir, err := resolveGitDir(ctx, root)
		if err == nil {
			if _, seen := gitDirs[gitDir]; seen {
				continue
			}
			if err = watcher.Add(gitDir); err != nil {
				// A failed Add can leave part of the directory registered.
				_ = watcher.Remove(gitDir)
			}
		}
		if err != nil {
			rootErr := fmt.Errorf("watch git metadata for %s: %w", root, err)
			if DescriptorsExhausted(err) {
				// Keeping the roots already watched would hold descriptors the
				// rest of the daemon (SQLite, locks, the Git commands of the
				// caller's poll) needs while there are none to spare. Release
				// them all and let the caller poll and back off.
				closeGitMetadataWatcher(watcher, gitDirs)
				return Subscription{}, errors.Join(append(rootErrors, rootErr)...)
			}
			rootErrors = append(rootErrors, rootErr)
			continue
		}
		gitDirs[gitDir] = struct{}{}
		for _, name := range gitMetadataFiles {
			targets[filepath.Join(gitDir, name)] = struct{}{}
		}
	}
	if len(gitDirs) == 0 {
		_ = watcher.Close()
		return Subscription{}, errors.Join(rootErrors...)
	}

	changes := make(chan struct{}, 1)
	degraded := make(chan struct{}, 1)
	if len(rootErrors) > 0 {
		degraded <- struct{}{}
	}
	go runGitMetadata(ctx, watcher, gitDirs, targets, changes, degraded)
	return Subscription{Changes: changes, Degraded: degraded, RootErrors: rootErrors}, nil
}

// gitMetadataCloseQuiet is how long closeGitMetadataWatcher waits without an
// event before closing the watcher.
const gitMetadataCloseQuiet = 100 * time.Millisecond

// closeGitMetadataWatcher closes watcher without leaking a descriptor. Git
// replaces HEAD and the index by renaming a lock file over them, and fsnotify's
// kqueue backend re-opens the new file from its event goroutine. Close
// releases only the descriptors registered when it starts, so a file re-opened
// while Close runs stays open for the life of the daemon. Removing the
// directory watches first stops new events, and draining until the backend is
// quiet lets any re-open already in flight finish, so Close releases it.
func closeGitMetadataWatcher(watcher *fsnotify.Watcher, gitDirs map[string]struct{}) {
	for gitDir := range gitDirs {
		_ = watcher.Remove(gitDir)
	}
drain:
	for {
		select {
		case _, ok := <-watcher.Events:
			if !ok {
				break drain
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				break drain
			}
		case <-time.After(gitMetadataCloseQuiet):
			break drain
		}
	}
	_ = watcher.Close()
}

func runGitMetadata(ctx context.Context, watcher *fsnotify.Watcher, gitDirs, targets map[string]struct{}, changes, degraded chan struct{}) {
	defer close(changes)
	defer close(degraded)
	defer closeGitMetadataWatcher(watcher, gitDirs)
	signal := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
			// The precise event may have been dropped.
			signal(changes)
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			name := filepath.Clean(event.Name)
			if _, ok := targets[name]; ok {
				signal(changes)
				continue
			}
			if _, ok := gitDirs[name]; ok && (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
				// The worktree's Git directory went away, so this root is no
				// longer covered until the caller rebuilds the watch.
				signal(changes)
				signal(degraded)
			}
		}
	}
}

// resolveGitDir returns root's Git directory: root/.git when it is a
// directory, the target of a linked worktree's "gitdir:" file, or, for a root
// inside a repository, what git rev-parse reports.
func resolveGitDir(ctx context.Context, root string) (string, error) {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	switch {
	case err == nil && info.IsDir():
		return dotGit, nil
	case err == nil:
		raw, readErr := os.ReadFile(dotGit)
		if readErr != nil {
			return "", fmt.Errorf("read %s: %w", dotGit, readErr)
		}
		gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !ok {
			return "", fmt.Errorf("%s is not a gitdir file", dotGit)
		}
		gitDir = strings.TrimSpace(gitDir)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
		return filepath.Clean(gitDir), nil
	case errors.Is(err, os.ErrNotExist):
		out, gitErr := aoprocess.CommandContext(ctx, "git", "-C", root, "rev-parse", "--absolute-git-dir").Output()
		if gitErr != nil {
			return "", fmt.Errorf("find git directory: %w", gitErr)
		}
		return filepath.Clean(strings.TrimSpace(string(out))), nil
	default:
		return "", fmt.Errorf("stat %s: %w", dotGit, err)
	}
}
