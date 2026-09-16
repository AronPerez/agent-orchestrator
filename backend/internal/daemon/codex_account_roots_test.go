//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A user who relocates AO home and leaves ~/.ao as a symlink must still be able
// to launch Codex: the credential-path validator rejects symlinked ancestors,
// so the daemon has to hand it the resolved state directory.
func TestCodexAccountRootsResolveSymlinkedStateDir(t *testing.T) {
	realStateDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "ao")
	if err := os.Symlink(realStateDir, stateDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	accounts, pending, staging := codexAccountRoots(stateDir)
	for name, root := range map[string]string{"accounts": accounts, "pending": pending, "staging": staging} {
		if !strings.HasPrefix(root, realStateDir+string(filepath.Separator)) {
			t.Errorf("%s root = %q, want it under the resolved state dir %q", name, root, realStateDir)
		}
	}
}
