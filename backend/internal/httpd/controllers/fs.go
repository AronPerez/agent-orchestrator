package controllers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// maxDirEntries bounds a single listing; deeper trees are reached by walking.
const maxDirEntries = 500

// FSController owns the read-only /fs routes used by remote clients to browse
// for a project path. Listing is directories-only and dotfile-free by design:
// the connection credential already authorizes spawning agents (shell access),
// so this reveals nothing new — but there is no reason to serve more than the
// picker needs.
type FSController struct {
	// Home supplies the default browse root; nil falls back to os.UserHomeDir.
	Home func() (string, error)
}

// Register mounts fs REST routes on the supplied router.
func (c *FSController) Register(r chi.Router) {
	r.Get("/fs/dirs", c.listDirs)
}

func (c *FSController) listDirs(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		home := os.UserHomeDir
		if c.Home != nil {
			home = c.Home
		}
		resolved, err := home()
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		dir = resolved
	}
	// The daemon judges the path by its own OS's rules — the client must never
	// pre-judge it, since a remote picker has no idea what a valid absolute path
	// looks like on the host it is browsing.
	if !filepath.IsAbs(dir) {
		envelope.WriteError(w, r, apierr.Invalid("FS_PATH_NOT_ABSOLUTE", "path must be absolute on the daemon host", nil))
		return
	}
	dir = filepath.Clean(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			envelope.WriteError(w, r, apierr.NotFound("FS_NOT_FOUND", "no such directory on the daemon host"))
		case errors.Is(err, os.ErrPermission):
			envelope.WriteError(w, r, apierr.Forbidden("FS_FORBIDDEN", "the daemon may not read that directory"))
		default:
			// ENOTDIR and friends: the path exists but is not browsable.
			envelope.WriteError(w, r, apierr.Invalid("FS_NOT_A_DIRECTORY", "not a directory", nil))
		}
		return
	}

	out := ListDirsResponse{Path: dir, Parent: filepath.Dir(dir), Entries: []FSEntry{}}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if len(out.Entries) == maxDirEntries {
			out.Truncated = true
			break
		}
		child := filepath.Join(dir, entry.Name())
		// .git may be a directory (normal clone) or a file (worktree): both are repos.
		_, gitErr := os.Stat(filepath.Join(child, ".git"))
		out.Entries = append(out.Entries, FSEntry{Name: entry.Name(), Path: child, GitRepo: gitErr == nil})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}
