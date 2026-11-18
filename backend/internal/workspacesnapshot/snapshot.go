package workspacesnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ErrDirty means at least one repository contains staged, unstaged, or
// untracked work. A dirty workspace has no revision that a completed
// verification can safely describe.
var ErrDirty = errors.New("workspace snapshot: repository has uncommitted changes")

type repository struct {
	name string
	path string
}

// Capture returns a deterministic identity for the exact committed state of
// every repository in a session workspace. It fails closed when any repository
// cannot be read or is dirty.
func Capture(ctx context.Context, root string, rows []domain.SessionWorktreeRecord) (string, error) {
	repositories := snapshotRepositories(root, rows)
	if len(repositories) == 0 {
		return "", errors.New("workspace snapshot: repository path is required")
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte("ao-workspace-snapshot-v1\x00"))
	for _, repo := range repositories {
		head, err := gitOutput(ctx, repo.path, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return "", fmt.Errorf("workspace snapshot: read %s HEAD: %w", repo.name, err)
		}
		status, err := gitOutput(ctx, repo.path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
		if err != nil {
			return "", fmt.Errorf("workspace snapshot: read %s status: %w", repo.name, err)
		}
		if len(status) != 0 {
			return "", fmt.Errorf("%w: %s", ErrDirty, repo.name)
		}
		_, _ = hash.Write([]byte(repo.name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.TrimSpace(string(head))))
		_, _ = hash.Write([]byte{0})
	}
	return "ws1:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func snapshotRepositories(root string, rows []domain.SessionWorktreeRecord) []repository {
	root = strings.TrimSpace(root)
	byName := make(map[string]repository, len(rows)+1)
	for _, row := range rows {
		name := strings.TrimSpace(row.RepoName)
		path := strings.TrimSpace(row.WorktreePath)
		if name == "" || path == "" {
			continue
		}
		byName[name] = repository{name: name, path: filepath.Clean(path)}
	}
	if root != "" {
		if _, ok := byName[domain.RootWorkspaceRepoName]; !ok {
			byName[domain.RootWorkspaceRepoName] = repository{name: domain.RootWorkspaceRepoName, path: filepath.Clean(root)}
		}
	}
	repositories := make([]repository, 0, len(byName))
	for _, repo := range byName {
		repositories = append(repositories, repo)
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].name < repositories[j].name })
	return repositories
}

func gitOutput(ctx context.Context, repo string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}
