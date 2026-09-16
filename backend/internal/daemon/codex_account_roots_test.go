//go:build !windows

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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
	// No account client factory is wired, so bootstrap still fails, but only
	// after the storage checks pass.
	err := svc.WaitCodexAccountBootstrap(context.Background())
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Details["reasonCode"] != "account_discovery_unavailable" {
		t.Fatalf("bootstrap error = %#v, want account_discovery_unavailable", err)
	}
}
