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
	BootID    string `json:"bootId,omitempty"`
}

type launchReady struct {
	Token   string `json:"token"`
	HostPID int    `json:"hostPid"`
	BootID  string `json:"bootId,omitempty"`
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
	bootID, err := currentBootIdentity()
	if err != nil {
		return "", fmt.Errorf("read system boot identity: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // Owner-only capability directory.
		return "", err
	}
	b, err := json.Marshal(launchIntent{Token: token, ParentPID: os.Getpid(), BootID: bootID})
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
	bootID, err := currentBootIdentity()
	if err != nil {
		return fmt.Errorf("read system boot identity: %w", err)
	}
	path, err := launchReadyPath(dataDir, sessionID, token)
	if err != nil {
		return err
	}
	b, err := json.Marshal(launchReady{Token: token, HostPID: hostPID, BootID: bootID})
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

// retirePriorBootHostOwnership removes ownership files only when every durable
// launch record proves that its host belonged to an earlier machine boot.
func retirePriorBootHostOwnership(dataDir, sessionID string) error {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect prior-boot chat host ownership: %w", ErrOwnershipInconclusive, err)
	}
	currentBootID, _ := currentBootIdentity()
	if currentBootID == "" {
		return nil
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
	if len(tokens) == 0 {
		return nil
	}
	stalePIDs := make(map[int]struct{}, len(tokens))
	for token := range tokens {
		ready, readyErr := readLaunchReady(dataDir, sessionID, token)
		if errors.Is(readyErr, os.ErrNotExist) {
			if probeErr := probeLaunchToken(dataDir, sessionID, token); probeErr == nil {
				delete(tokens, token)
				continue
			}
			return nil // The normal descriptor/absent-host path retains the fence.
		}
		if readyErr != nil {
			return fmt.Errorf("%w: inspect prior-boot chat host readiness: %w", ErrOwnershipInconclusive, readyErr)
		}
		if !priorBootIdentity(ready.BootID, currentBootID) {
			return nil
		}
		stalePIDs[ready.HostPID] = struct{}{}
	}
	if len(stalePIDs) == 0 {
		return nil
	}
	if d, readErr := readDescriptorOnce(dataDir, sessionID); readErr == nil {
		if _, ok := stalePIDs[d.PID]; !ok {
			return nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect prior-boot chat host descriptor: %w", ErrOwnershipInconclusive, readErr)
	}
	lock, _ := lockPath(dataDir, sessionID)
	if b, readErr := os.ReadFile(lock); readErr == nil { //nolint:gosec // AO-owned path derived from validated session id.
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(b)))
		if parseErr != nil {
			return fmt.Errorf("%w: invalid prior-boot chat host lock", ErrOwnershipInconclusive)
		}
		if _, ok := stalePIDs[pid]; !ok {
			return nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect prior-boot chat host lock: %w", ErrOwnershipInconclusive, readErr)
	}
	descriptor, _ := descriptorPath(dataDir, sessionID)
	if err := os.Remove(descriptor); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retire prior-boot chat host descriptor: %w", err)
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retire prior-boot chat host lock: %w", err)
	}
	for token := range tokens {
		clearLaunchIntent(dataDir, sessionID, token)
	}
	return nil
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
	return probeLaunchTokenWithProbes(
		dataDir, sessionID, token,
		processalive.Exited,
		currentBootIdentity,
		providerTreeDiesWithHost(),
	)
}

func probeLaunchTokenWithExitProbe(
	dataDir, sessionID, token string,
	exited func(int) bool,
) error {
	return probeLaunchTokenWithProbes(
		dataDir, sessionID, token,
		exited,
		currentBootIdentity,
		providerTreeDiesWithHost(),
	)
}

func probeLaunchTokenWithProbes(
	dataDir, sessionID, token string,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
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
		return settleAdmittedHostOwnership(
			dataDir, sessionID, token, ready,
			exited, bootIdentity, providerTreeDies,
		)
	}
	if intentErr == nil {
		currentBootID, bootErr := bootIdentity()
		if bootErr == nil && priorBootIdentity(intent.BootID, currentBootID) {
			clearLaunchIntent(dataDir, sessionID, token)
			return nil
		}
		if !exited(intent.ParentPID) {
			return fmt.Errorf("%w: chat host launcher %d has not published readiness", ErrOwnershipInconclusive, intent.ParentPID)
		}
		// The launcher cannot publish after Exited reports conclusive absence.
		// Re-read after that proof so a readiness file published between the
		// first read and the exit observation still fences the admitted child.
		ready, readyErr = readLaunchReady(dataDir, sessionID, token)
		if readyErr == nil {
			return settleAdmittedHostOwnership(
				dataDir, sessionID, token, ready,
				exited, bootIdentity, providerTreeDies,
			)
		}
		if !errors.Is(readyErr, os.ErrNotExist) {
			return fmt.Errorf("%w: re-read chat host launch readiness: %w", ErrOwnershipInconclusive, readyErr)
		}
		clearLaunchIntent(dataDir, sessionID, token)
	}
	return nil
}

func settleAdmittedHostOwnership(
	dataDir, sessionID, token string,
	ready launchReady,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
) error {
	if err := admittedHostOwnershipError(ready, exited, bootIdentity, providerTreeDies); err != nil {
		return err
	}
	clearLaunchIntent(dataDir, sessionID, token)
	return nil
}

func admittedHostOwnershipError(
	ready launchReady,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
) error {
	currentBootID, bootErr := bootIdentity()
	if bootErr == nil && priorBootIdentity(ready.BootID, currentBootID) {
		// Neither a host nor its provider tree can survive an OS reboot. Check
		// this before the PID probe because the old host PID may already have
		// been reused by an unrelated process on the new boot.
		return nil
	}
	if !exited(ready.HostPID) {
		return fmt.Errorf("%w: unpublished chat host process %d is not proven absent", ErrOwnershipInconclusive, ready.HostPID)
	}
	if providerTreeDies {
		// Windows puts the provider tree in a kill-on-close Job Object owned by
		// the host. A conclusively exited host therefore proves tree absence.
		return nil
	}
	if ready.BootID == "" {
		return fmt.Errorf("%w: admitted chat host %d exited with no durable boot identity", ErrOwnershipInconclusive, ready.HostPID)
	}
	if bootErr != nil {
		return fmt.Errorf("%w: verify boot identity after admitted chat host %d exited: %w", ErrOwnershipInconclusive, ready.HostPID, bootErr)
	}
	// The host can die after starting its Unix provider process group. On the
	// same boot, only orderly host teardown can prove provider-tree cleanup by
	// clearing the readiness record.
	return fmt.Errorf("%w: admitted chat host %d exited on the current boot without proving provider-tree cleanup", ErrOwnershipInconclusive, ready.HostPID)
}

func priorBootIdentity(stored, current string) bool {
	storedSource, storedValue, storedOK := strings.Cut(stored, ":")
	currentSource, currentValue, currentOK := strings.Cut(current, ":")
	return storedOK && currentOK &&
		storedSource == currentSource &&
		storedValue != "" && currentValue != "" &&
		stored != current
}
