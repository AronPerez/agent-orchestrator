//go:build !windows

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
)

// A user who relocates AO home and leaves ~/.ao as a symlink must still be able
// to launch Codex: the credential-path validator rejects symlinked ancestors,
// so the daemon has to hand it the resolved state directory.
func TestCodexAccountRootsResolveSymlinkedStateDir(t *testing.T) {
	realStateDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "ao")
	if err := os.Symlink(realStateDir, stateDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	accounts, pending, staging := codexAccountRoots(stateDir)
	svc := agentsvc.NewWithDeps(agentsvc.Deps{
		Context:          context.Background(),
		CodexAccountRoot: accounts, CodexPendingRoot: pending, CodexSwitchStagingRoot: staging,
		CodexGlobalHome: t.TempDir(),
	})
	// Unresolved, these roots sit under a symlinked ancestor and setup fails
	// account_storage_unsafe with retryable=false, blocking Codex until restart.
	if err := svc.WaitCodexAccountStoreReady(context.Background()); err != nil {
		t.Fatalf("account store setup failed: %#v", err)
	}
}
