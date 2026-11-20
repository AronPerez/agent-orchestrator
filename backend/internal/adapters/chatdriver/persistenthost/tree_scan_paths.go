//go:build darwin || linux

package persistenthost

import (
	"path/filepath"
	"strings"
)

// worktreeRoots returns workdir as given and as resolved, since the kernel
// reports working directories by their real path (/private/var, not /var).
func worktreeRoots(workdir string) []string {
	roots := []string{filepath.Clean(workdir)}
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil && resolved != roots[0] {
		roots = append(roots, resolved)
	}
	return roots
}

// withinWorktree reports whether path is one of roots or lies below one.
func withinWorktree(roots []string, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
