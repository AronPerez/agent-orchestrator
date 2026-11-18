//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestAdmittedHostExitKeepsFenceWhileProviderSurvives(t *testing.T) {
	dataDir := t.TempDir()
	sessionID := "orphan-provider"
	pidFile := filepath.Join(t.TempDir(), "provider.pid")
	host := exec.Command("sh", "-c", `nohup sleep 30 >/dev/null 2>&1 & echo $! > "$1"`, "sh", pidFile) //nolint:gosec // Fixed test helper command.
	if err := host.Run(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile) //nolint:gosec // Test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	providerPID, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(providerPID, syscall.SIGKILL) })
	if !processalive.Alive(providerPID) {
		t.Fatalf("provider %d did not survive host exit", providerPID)
	}

	token, err := reserveLaunchIntent(dataDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clearLaunchIntent(dataDir, sessionID, token) })
	if err := publishLaunchReady(dataDir, sessionID, token, host.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := probeLaunchToken(dataDir, sessionID, token); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatalf("probe error = %v, want possible orphan provider ownership", err)
	}
	if _, err := readLaunchReady(dataDir, sessionID, token); err != nil {
		t.Fatalf("probe removed readiness while provider %d survives: %v", providerPID, err)
	}
}

func TestPublishedHostExitKeepsFenceWhileProviderSurvives(t *testing.T) {
	cfg := Config{
		SessionID: "published-orphan-provider", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env: append(os.Environ(),
			"AO_CHAT_HOST_PROVIDER_HELPER=1",
			"AO_CHAT_HOST_IGNORE_STDIN=1",
		),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	providerPID := requestProviderPID(t, transport, 1, "pid")
	t.Cleanup(func() {
		_ = transport.Stdin.Close()
		_ = syscall.Kill(-providerPID, syscall.SIGKILL)
		_ = syscall.Kill(-d.PID, syscall.SIGKILL)
	})

	if err := syscall.Kill(d.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !processalive.Exited(d.PID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !processalive.Exited(d.PID) {
		t.Fatalf("chat host %d did not exit", d.PID)
	}
	if !processalive.Alive(providerPID) {
		t.Fatalf("provider %d did not survive published host exit", providerPID)
	}

	if err := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatalf("Shutdown() error = %v, want orphan provider ownership inconclusive", err)
	}
	if !processalive.Alive(providerPID) {
		t.Fatalf("provider %d exited before ownership was fenced", providerPID)
	}
}

func TestDetachedChatHostsAreReapedAfterRepeatedShutdown(t *testing.T) {
	cfg := Config{
		SessionID: "reaped-host", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
	for range 3 {
		transport, err := ConnectOrStart(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		_ = transport.Stdin.Close()
		if err := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			if err := syscall.Kill(d.PID, 0); errors.Is(err, syscall.ESRCH) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("detached chat host %d remained a child after shutdown", d.PID)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestShutdownQuickSignalsSooner(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quick    bool
		min, max time.Duration
	}{
		{name: "quick stop", quick: true, min: providerQuickGrace, max: providerStdinGrace},
		{name: "full grace", quick: false, min: providerStdinGrace, max: providerStopDeadline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				SessionID: "stdin-ignorer", DataDir: t.TempDir(), Workdir: t.TempDir(),
				Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1", "AO_CHAT_HOST_IGNORE_STDIN=1"),
				Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
			}
			t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
			transport, err := ConnectOrStart(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			_ = transport.Stdin.Close()
			started := time.Now()
			stop := Shutdown
			if tc.quick {
				stop = ShutdownQuick
			}
			if err := stop(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(started); took < tc.min || took >= tc.max {
				t.Fatalf("shutdown took %s, want within [%s, %s)", took, tc.min, tc.max)
			}
		})
	}
}
