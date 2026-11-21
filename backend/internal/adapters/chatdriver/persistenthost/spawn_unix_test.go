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

func processGroupGone(pgid int) bool {
	return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

func launchRecords(t *testing.T, cfg Config) []string {
	t.Helper()
	dir, err := hostDir(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, launchIntentPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestHostSIGTERMRunsOrderlyTeardown(t *testing.T) {
	cfg, _, d, providerPID := startFencedHost(t, "sigterm-host")
	if len(launchRecords(t, cfg)) == 0 {
		t.Fatal("running host holds no launch records")
	}

	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "chat host exit", func() bool { return processalive.Exited(d.PID) })

	if !processGroupGone(providerPID) {
		t.Fatalf("provider group %d survived SIGTERM to its host", providerPID)
	}
	if left := launchRecords(t, cfg); len(left) != 0 {
		t.Fatalf("SIGTERM left launch records behind: %v", left)
	}
	next, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ConnectOrStart after orderly SIGTERM teardown: %v", err)
	}
	t.Cleanup(func() { _ = next.Stdin.Close() })
	nextProvider := requestProviderPID(t, next, 2, "pid")
	t.Cleanup(func() { _ = syscall.Kill(-nextProvider, syscall.SIGKILL) })
	if err := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
		t.Fatalf("Shutdown of the replacement host: %v", err)
	}
}

// A hard kill skips orderly teardown. No process scan can prove that every
// process the provider started is gone (a detached system shell can leave the
// host's session, hide its environment and work outside the worktree), so the
// session stays fenced until reboot even when the provider group is empty.
func TestKilledHostStaysFencedEvenWhenProviderGroupIsGone(t *testing.T) {
	cfg, transport, d, providerPID := startFencedHost(t, "sigkill-host")

	if err := syscall.Kill(d.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "chat host exit", func() bool { return processalive.Exited(d.PID) })
	_ = transport.Stdin.Close()
	waitUntil(t, "provider group exit", func() bool { return processGroupGone(providerPID) })

	_, err := ConnectOrStart(context.Background(), cfg)
	if !errors.Is(err, ErrOwnershipInconclusive) ||
		!strings.Contains(err.Error(), "exited on the current boot without proving provider-tree cleanup") {
		t.Fatalf("ConnectOrStart error = %v, want the hard-killed host to stay fenced", err)
	}
	if len(launchRecords(t, cfg)) == 0 {
		t.Fatal("refused recovery removed the launch records")
	}
}

// startStderrHolder runs, from the provider helper, a child in a process group
// of its own that keeps the provider's stderr open after the provider exits,
// so the host's final child.Wait blocks on its stderr copy.
func startStderrHolder(pidFile string) {
	holder := exec.Command("sleep", "30") //nolint:gosec // Fixed test helper command.
	holder.Stderr = os.Stderr
	holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := holder.Start(); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(holder.Process.Pid)), 0o600); err != nil {
		os.Exit(3)
	}
}

func TestSecondSignalEndsHostStuckInTeardown(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "holder.pid")
	cfg, _, d, _ := startFencedHost(t, "stuck-teardown", "AO_CHAT_HOST_STDERR_HOLDER_PID_FILE="+pidFile)
	var holderPID int
	waitUntil(t, "stderr holder start", func() bool {
		b, _ := os.ReadFile(pidFile) //nolint:gosec // Test-owned temporary path.
		holderPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return holderPID > 0
	})
	t.Cleanup(func() { _ = syscall.Kill(holderPID, syscall.SIGKILL) })

	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// Teardown stops the provider group, then waits on the stderr copy the
	// holder keeps open.
	time.Sleep(2 * time.Second)
	if processalive.Exited(d.PID) {
		t.Fatal("host finished teardown; the stderr holder did not reproduce a stuck teardown")
	}

	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "stuck chat host exit after a second signal", func() bool { return processalive.Exited(d.PID) })
	if len(launchRecords(t, cfg)) == 0 {
		t.Fatal("abrupt exit after a second signal cleared the launch records")
	}
	if _, err := ConnectOrStart(context.Background(), cfg); !errors.Is(err, ErrOwnershipInconclusive) {
		t.Fatalf("ConnectOrStart error = %v, want the unfinished teardown to keep the fence", err)
	}
}
