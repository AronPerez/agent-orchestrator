package persistenthost

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

const (
	launchIDEnv        = "AO_CHAT_HOST_LAUNCH_ID"
	launchIntentPrefix = "host.launch."
	launchIntentSuffix = ".json"
	launchReadySuffix  = ".ready.json"
)

type launchIntent struct {
	Token     string `json:"token"`
	ParentPID int    `json:"parentPid"`
}

type launchReady struct {
	Token   string `json:"token"`
	HostPID int    `json:"hostPid"`
}

func validLaunchToken(token string) bool {
	b, err := hex.DecodeString(token)
	return err == nil && len(b) == 32
}

func launchIntentPath(dataDir, sessionID, token string) (string, error) {
	if !validLaunchToken(token) {
		return "", errors.New("invalid chat host launch token")
	}
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, launchIntentPrefix+token+launchIntentSuffix), nil
}

func launchReadyPath(dataDir, sessionID, token string) (string, error) {
	path, err := launchIntentPath(dataDir, sessionID, token)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, launchIntentSuffix) + launchReadySuffix, nil
}

// The intent exists before process creation. The detached child cannot start
// its provider until the parent atomically publishes a matching readiness file
// with the child's exact PID. Recovery therefore has an ownership fence during
// the whole descriptor- and lock-free launch window.
func reserveLaunchIntent(dataDir, sessionID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	path, err := launchIntentPath(dataDir, sessionID, token)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // Owner-only capability directory.
		return "", err
	}
	b, err := json.Marshal(launchIntent{Token: token, ParentPID: os.Getpid()})
	if err != nil {
		return "", err
	}
	if err := writeAtomicLaunchFile(path, b); err != nil {
		return "", err
	}
	return token, nil
}

func publishLaunchReady(dataDir, sessionID, token string, hostPID int) error {
	intent, err := readLaunchIntent(dataDir, sessionID, token)
	if err != nil {
		return err
	}
	if intent.Token != token || intent.ParentPID != os.Getpid() || hostPID <= 0 {
		return errors.New("chat host launch intent ownership changed")
	}
	path, err := launchReadyPath(dataDir, sessionID, token)
	if err != nil {
		return err
	}
	b, err := json.Marshal(launchReady{Token: token, HostPID: hostPID})
	if err != nil {
		return err
	}
	return writeAtomicLaunchFile(path, b)
}

func writeAtomicLaunchFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".host.launch-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("chat host launch readiness already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readLaunchIntent(dataDir, sessionID, token string) (launchIntent, error) {
	path, err := launchIntentPath(dataDir, sessionID, token)
	if err != nil {
		return launchIntent{}, err
	}
	var intent launchIntent
	if err := readLaunchJSON(path, &intent); err != nil {
		return launchIntent{}, err
	}
	if intent.Token != token || intent.ParentPID <= 0 {
		return launchIntent{}, errors.New("invalid chat host launch intent")
	}
	return intent, nil
}

func readLaunchReady(dataDir, sessionID, token string) (launchReady, error) {
	path, err := launchReadyPath(dataDir, sessionID, token)
	if err != nil {
		return launchReady{}, err
	}
	var ready launchReady
	if err := readLaunchJSON(path, &ready); err != nil {
		return launchReady{}, err
	}
	if ready.Token != token || ready.HostPID <= 0 {
		return launchReady{}, errors.New("invalid chat host launch readiness")
	}
	return ready, nil
}

func readLaunchJSON(path string, out any) error {
	f, err := openHostFile(path)
	if err != nil {
		return err
	}
	b, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	return json.Unmarshal(b, out)
}

func clearLaunchIntent(dataDir, sessionID, token string) {
	if path, err := launchReadyPath(dataDir, sessionID, token); err == nil {
		_ = os.Remove(path)
	}
	if path, err := launchIntentPath(dataDir, sessionID, token); err == nil {
		_ = os.Remove(path)
	}
}

func awaitLaunchReady(ctx context.Context, cfg Config) error {
	if cfg.launchToken == "" {
		return nil
	}
	deadline := time.NewTimer(startupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		intent, intentErr := readLaunchIntent(cfg.DataDir, cfg.SessionID, cfg.launchToken)
		if intentErr == nil {
			ready, readyErr := readLaunchReady(cfg.DataDir, cfg.SessionID, cfg.launchToken)
			if readyErr == nil {
				if ready.HostPID != os.Getpid() {
					return errors.New("chat host launch readiness names another process")
				}
				return nil
			}
			if !errors.Is(readyErr, os.ErrNotExist) {
				return readyErr
			}
			if processalive.Exited(intent.ParentPID) {
				return errors.New("chat host launcher exited before publishing readiness")
			}
		} else if !errors.Is(intentErr, os.ErrNotExist) {
			return intentErr
		} else {
			return errors.New("chat host launch intent disappeared before readiness")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("timed out waiting for chat host launch readiness")
		case <-ticker.C:
		}
	}
}

func launchTokenFromEnv(env []string) (string, []string) {
	if env == nil {
		return "", nil
	}
	token := ""
	clean := make([]string, 0, len(env))
	prefix := launchIDEnv + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			token = strings.TrimPrefix(entry, prefix)
			continue
		}
		clean = append(clean, entry)
	}
	return token, clean
}

func envWithLaunchToken(env []string, token string) []string {
	if env == nil {
		env = os.Environ()
	}
	_, clean := launchTokenFromEnv(env)
	return append(clean, launchIDEnv+"="+token)
}

func probeUnpublishedHost(dataDir, sessionID string) error {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: read unpublished chat host ownership: %w", ErrOwnershipInconclusive, err)
	}
	tokens := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, launchIntentPrefix) {
			continue
		}
		token := strings.TrimPrefix(name, launchIntentPrefix)
		switch {
		case strings.HasSuffix(token, launchReadySuffix):
			token = strings.TrimSuffix(token, launchReadySuffix)
		case strings.HasSuffix(token, launchIntentSuffix):
			token = strings.TrimSuffix(token, launchIntentSuffix)
		default:
			continue
		}
		if validLaunchToken(token) {
			tokens[token] = struct{}{}
		}
	}
	for token := range tokens {
		if err := probeLaunchToken(dataDir, sessionID, token); err != nil {
			return err
		}
	}
	path, _ := lockPath(dataDir, sessionID)
	b, err := os.ReadFile(path) //nolint:gosec // AO-owned path derived from validated session id.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: read unpublished chat host lock: %w", ErrOwnershipInconclusive, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("%w: invalid unpublished chat host lock", ErrOwnershipInconclusive)
	}
	if !processalive.Exited(pid) {
		return fmt.Errorf("%w: unpublished chat host process %d is not proven absent", ErrOwnershipInconclusive, pid)
	}
	return nil
}

func probeLaunchToken(dataDir, sessionID, token string) error {
	return probeLaunchTokenWithExitProbe(dataDir, sessionID, token, processalive.Exited)
}

func probeLaunchTokenWithExitProbe(
	dataDir, sessionID, token string,
	exited func(int) bool,
) error {
	intent, intentErr := readLaunchIntent(dataDir, sessionID, token)
	ready, readyErr := readLaunchReady(dataDir, sessionID, token)
	if intentErr != nil && !errors.Is(intentErr, os.ErrNotExist) {
		return fmt.Errorf("%w: read chat host launch intent: %w", ErrOwnershipInconclusive, intentErr)
	}
	if readyErr != nil && !errors.Is(readyErr, os.ErrNotExist) {
		return fmt.Errorf("%w: read chat host launch readiness: %w", ErrOwnershipInconclusive, readyErr)
	}
	if readyErr == nil {
		return admittedHostOwnershipError(ready.HostPID, exited)
	}
	if intentErr == nil {
		if !exited(intent.ParentPID) {
			return fmt.Errorf("%w: chat host launcher %d has not published readiness", ErrOwnershipInconclusive, intent.ParentPID)
		}
		// The launcher cannot publish after Exited reports conclusive absence.
		// Re-read after that proof so a readiness file published between the
		// first read and the exit observation still fences the admitted child.
		ready, readyErr = readLaunchReady(dataDir, sessionID, token)
		if readyErr == nil {
			return admittedHostOwnershipError(ready.HostPID, exited)
		}
		if !errors.Is(readyErr, os.ErrNotExist) {
			return fmt.Errorf("%w: re-read chat host launch readiness: %w", ErrOwnershipInconclusive, readyErr)
		}
		clearLaunchIntent(dataDir, sessionID, token)
	}
	return nil
}

func admittedHostOwnershipError(hostPID int, exited func(int) bool) error {
	if exited(hostPID) {
		// The host can die after starting its Unix provider process group but
		// before publishing host.json. Only the host can prove orderly provider
		// teardown by clearing this readiness record; host exit alone is not proof.
		return fmt.Errorf("%w: admitted chat host %d exited without proving provider-tree cleanup", ErrOwnershipInconclusive, hostPID)
	}
	return fmt.Errorf("%w: unpublished chat host process %d is not proven absent", ErrOwnershipInconclusive, hostPID)
}
