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

// startFencedHost starts a real detached host, as recovery sees it in
// production: it holds launch readiness for its whole life.
func startFencedHost(t *testing.T, sessionID string, extraEnv ...string) (Config, *Transport, Descriptor, int) {
	t.Helper()
	cfg := Config{
		SessionID: sessionID, DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"), extraEnv...),
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
		_ = syscall.Kill(d.PID, syscall.SIGKILL)
		_ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID)
	})
	return cfg, transport, d, providerPID
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func launchRecords(t *testing.T, cfg Config, suffix string) []string {
	t.Helper()
	dir, err := hostDir(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, launchIntentPrefix+"*"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func assertResumes(t *testing.T, cfg Config) {
	t.Helper()
	next, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ConnectOrStart after the old provider tree was gone: %v", err)
	}
	t.Cleanup(func() { _ = next.Stdin.Close() })
	providerPID := requestProviderPID(t, next, 2, "pid")
	t.Cleanup(func() { _ = syscall.Kill(-providerPID, syscall.SIGKILL) })
	if err := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
		t.Fatalf("Shutdown of the replacement host: %v", err)
	}
}

func TestHostSIGTERMRunsOrderlyTeardown(t *testing.T) {
	cfg, _, d, providerPID := startFencedHost(t, "sigterm-host")
	if len(launchRecords(t, cfg, launchProviderSuffix)) != 1 {
		t.Fatal("running host did not record its provider process group")
	}

	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "chat host exit", func() bool { return processalive.Exited(d.PID) })

	if !processGroupExited(providerPID) {
		t.Fatalf("provider group %d survived SIGTERM to its host", providerPID)
	}
	for _, suffix := range []string{launchIntentSuffix, launchReadySuffix, launchProviderSuffix} {
		if left := launchRecords(t, cfg, suffix); len(left) != 0 {
			t.Fatalf("SIGTERM left launch records behind: %v", left)
		}
	}
	assertResumes(t, cfg)
}

func TestKilledHostResumesOnceProviderGroupIsGone(t *testing.T) {
	cfg, transport, d, providerPID := startFencedHost(t, "sigkill-host")

	if err := syscall.Kill(d.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "chat host exit", func() bool { return processalive.Exited(d.PID) })
	_ = transport.Stdin.Close()
	// The provider normally exits on its own once the host's pipes close.
	waitUntil(t, "provider group exit", func() bool { return processGroupExited(providerPID) })
	stale := append(launchRecords(t, cfg, launchReadySuffix), launchRecords(t, cfg, launchProviderSuffix)...)
	if len(stale) != 2 {
		t.Fatalf("SIGKILL left launch records %v, want readiness and provider records for recovery to settle", stale)
	}

	assertResumes(t, cfg)
	for _, path := range stale {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old launch record %s survived recovery: %v", path, err)
		}
	}
}

func TestKilledHostStaysFencedWhileProviderGroupLives(t *testing.T) {
	cfg, _, d, providerPID := startFencedHost(t, "sigkill-live-provider", "AO_CHAT_HOST_IGNORE_STDIN=1")

	if err := syscall.Kill(d.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "chat host exit", func() bool { return processalive.Exited(d.PID) })
	if processGroupExited(providerPID) {
		t.Fatalf("provider group %d did not survive its host", providerPID)
	}

	_, err := ConnectOrStart(context.Background(), cfg)
	if !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatalf("ConnectOrStart error = %v, want the live provider group to keep the fence", err)
	}
	if !strings.Contains(err.Error(), "provider process group "+strconv.Itoa(providerPID)+" is not proven absent") {
		t.Fatalf("ConnectOrStart error = %v, want it to name the live provider group", err)
	}
	if len(launchRecords(t, cfg, launchReadySuffix)) != 1 || len(launchRecords(t, cfg, launchProviderSuffix)) != 1 {
		t.Fatal("refused recovery removed the launch records")
	}
	if !processalive.Alive(providerPID) {
		t.Fatalf("refused recovery stopped provider %d", providerPID)
	}
}
