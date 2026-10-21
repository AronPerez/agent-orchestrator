//go:build !windows

package acp

import (
	"bufio"
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
)

func TestPersistentTerminateWaitsForProviderGrace(t *testing.T) {
	dataDir := t.TempDir()
	const sessionID = "shutdown-grace"
	transport, err := persistenthost.ConnectOrStart(t.Context(), persistenthost.Config{
		SessionID: sessionID, DataDir: dataDir, Workdir: t.TempDir(), Env: os.Environ(),
		Argv: []string{"sh", "-c", "trap '' TERM; printf 'ready\\n'; while :; do sleep 1; done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	shutdown := func(ctx context.Context, quick bool) error {
		if quick {
			return persistenthost.ShutdownQuick(ctx, dataDir, sessionID)
		}
		return persistenthost.Shutdown(ctx, dataDir, sessionID)
	}
	t.Cleanup(func() { _ = shutdown(context.Background(), false) })
	// Wait until the provider ignores SIGTERM, so termination needs both grace periods.
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(transport.Stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("provider readiness = %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not become ready")
	}

	terminate := persistentTerminate(newGatedReader(transport.Stdout, false), transport.Stdin, &atomic.Bool{}, shutdown)
	started := time.Now()
	if err := terminate(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 5*time.Second {
		t.Fatalf("termination took %s, did not exercise the full provider grace periods", elapsed)
	}
}
