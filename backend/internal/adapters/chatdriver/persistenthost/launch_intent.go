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
	launchIDEnv          = "AO_CHAT_HOST_LAUNCH_ID"
	launchIntentPrefix   = "host.launch."
	launchIntentSuffix   = ".json"
	launchReadySuffix    = ".ready.json"
	launchProviderSuffix = ".provider.json"
	// providerTreeEnv marks every process the provider starts, so recovery can
	// find work that left the provider's process group or session.
	providerTreeEnv = "AO_CHAT_HOST_TREE"
)

// clearLaunchIntentRetryDelays bounds how long a host keeps retrying the
// ownership lock before it leaves its launch records behind. A record left
// behind fails closed, so the retry only narrows that window.
var clearLaunchIntentRetryDelays = []time.Duration{50 * time.Millisecond, 250 * time.Millisecond, time.Second}

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

// launchProvider records how to find the provider tree an admitted host
// started: the provider's process group, the marker in the environment of
// every process the provider starts, and the worktree those processes work in. After an abrupt host exit on the same
// boot it is the evidence recovery uses to prove the whole tree is gone.
type launchProvider struct {
	Token        string `json:"token"`
	HostPID      int    `json:"hostPid"`
	ProviderPGID int    `json:"providerPgid"`
	TreeMarker   string `json:"treeMarker"`
	Workdir      string `json:"workdir"`
	BootID       string `json:"bootId,omitempty"`
}

// launchTokenFromName returns the launch token a host-directory entry belongs
// to, for intent, readiness and provider records alike.
func launchTokenFromName(name string) (string, bool) {
	if !strings.HasPrefix(name, launchIntentPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, launchIntentPrefix)
	// The bare intent suffix is a suffix of the others, so it is tried last.
	for _, suffix := range []string{launchReadySuffix, launchProviderSuffix, launchIntentSuffix} {
		if token, ok := strings.CutSuffix(rest, suffix); ok {
			return token, validLaunchToken(token)
		}
	}
	return "", false
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

func launchProviderPath(dataDir, sessionID, token string) (string, error) {
	path, err := launchIntentPath(dataDir, sessionID, token)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, launchIntentSuffix) + launchProviderSuffix, nil
}

// publishLaunchProvider records the provider process group of the admitted
// host that is calling it. It runs after the provider starts, so a host that
// dies before the record exists stays fenced.
func publishLaunchProvider(
	dataDir, sessionID, token string,
	providerPGID int, treeMarker, workdir string,
) (retErr error) {
	if providerPGID <= 1 {
		return errors.New("invalid chat host provider process group")
	}
	if !validLaunchToken(treeMarker) {
		return errors.New("invalid chat host provider tree marker")
	}
	if !filepath.IsAbs(workdir) {
		return errors.New("chat host provider workdir must be absolute")
	}
	ownershipLock, err := acquireHostOwnershipMutationLock(dataDir, sessionID)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := ownershipLock.Release(); releaseErr != nil {
			retErr = errors.Join(retErr, releaseErr)
		}
	}()
	ready, err := readLaunchReady(dataDir, sessionID, token)
	if err != nil {
		return err
	}
	if ready.HostPID != os.Getpid() {
		return errors.New("chat host launch readiness names another process")
	}
	path, err := launchProviderPath(dataDir, sessionID, token)
	if err != nil {
		return err
	}
	b, err := json.Marshal(launchProvider{
		Token: token, HostPID: ready.HostPID, ProviderPGID: providerPGID,
		TreeMarker: treeMarker, Workdir: workdir, BootID: ready.BootID,
	})
	if err != nil {
		return err
	}
	return writeAtomicLaunchFile(path, b)
}

func readLaunchProvider(dataDir, sessionID, token string) (launchProvider, error) {
	path, err := launchProviderPath(dataDir, sessionID, token)
	if err != nil {
		return launchProvider{}, err
	}
	var provider launchProvider
	if err := readLaunchJSON(path, &provider); err != nil {
		return launchProvider{}, err
	}
	if provider.Token != token || provider.HostPID <= 0 || provider.ProviderPGID <= 1 ||
		!validLaunchToken(provider.TreeMarker) || !filepath.IsAbs(provider.Workdir) {
		return launchProvider{}, errors.New("invalid chat host provider record")
	}
	return provider, nil
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
	return publishLaunchReadyAfterWrite(dataDir, sessionID, token, hostPID, nil)
}

func publishLaunchReadyAfterWrite(
	dataDir, sessionID, token string,
	hostPID int,
	afterWrite func(),
) (retErr error) {
	ownershipLock, err := acquireHostOwnershipMutationLock(dataDir, sessionID)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := ownershipLock.Release(); releaseErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("%w: release chat host readiness publication: %w", ErrOwnershipInconclusive, releaseErr))
		}
	}()
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
	ready := launchReady{Token: token, HostPID: hostPID, BootID: bootID}
	b, err := json.Marshal(ready)
	if err != nil {
		return err
	}
	if writeErr := writeAtomicLaunchFile(path, b); writeErr != nil {
		current, confirmErr := readLaunchReady(dataDir, sessionID, token)
		switch {
		case confirmErr == nil && current == ready:
			// The filesystem reported an uncertain write outcome after publishing
			// the exact record. The child may already have consumed it.
		case errors.Is(confirmErr, os.ErrNotExist):
			return writeErr
		default:
			return errors.Join(
				fmt.Errorf("%w: confirm chat host readiness after write failure", ErrOwnershipInconclusive),
				writeErr,
				confirmErr,
			)
		}
	}
	if afterWrite != nil {
		afterWrite()
	}
	return nil
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
	_ = clearLaunchIntentWith(
		dataDir, sessionID, token,
		acquireHostOwnershipMutationLock, clearLaunchIntentRetryDelays,
	)
}

// clearLaunchIntentWith retries a failed ownership lock (for example a
// transient EMFILE) before giving up. Giving up leaves the records in place,
// which keeps the session fenced rather than guessing.
func clearLaunchIntentWith(
	dataDir, sessionID, token string,
	acquire func(string, string) (hostOwnershipMutationLock, error),
	delays []time.Duration,
) error {
	for attempt := 0; ; attempt++ {
		ownershipLock, err := acquire(dataDir, sessionID)
		if err == nil {
			clearLaunchIntentUnlocked(dataDir, sessionID, token)
			return ownershipLock.Release()
		}
		if attempt >= len(delays) {
			return err
		}
		time.Sleep(delays[attempt])
	}
}

func clearLaunchIntentUnlocked(dataDir, sessionID, token string) {
	if path, err := launchIntentPath(dataDir, sessionID, token); err == nil {
		_ = os.Remove(path)
	}
	if path, err := launchReadyPath(dataDir, sessionID, token); err == nil {
		_ = os.Remove(path)
	}
	// The provider record is only meaningful beside readiness, so it goes last.
	// A stray one is swept by the next probe of the host directory.
	if path, err := launchProviderPath(dataDir, sessionID, token); err == nil {
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

// envWithProviderTreeMarker gives the provider this host's tree marker in
// place of any marker inherited from an enclosing tree.
func envWithProviderTreeMarker(env []string, marker string) []string {
	if env == nil {
		env = os.Environ()
	}
	prefix := providerTreeEnv + "="
	marked := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			marked = append(marked, entry)
		}
	}
	return append(marked, prefix+marker)
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
	return retirePriorBootHostOwnershipAfterValidation(dataDir, sessionID, nil)
}

func retirePriorBootHostOwnershipAfterValidation(dataDir, sessionID string, afterValidation func()) error {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("%w: inspect prior-boot chat host directory: %w", ErrOwnershipInconclusive, err)
	}
	ownershipLock, err := acquireHostOwnershipMutationLock(dataDir, sessionID)
	if err != nil {
		return err
	}
	defer func() { _ = ownershipLock.Release() }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%w: inspect prior-boot chat host ownership: %w", ErrOwnershipInconclusive, err)
	}
	currentBootID, _ := currentBootIdentity()
	if currentBootID == "" {
		return nil
	}
	tokens := make(map[string]struct{})
	for _, entry := range entries {
		if token, ok := launchTokenFromName(entry.Name()); ok {
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
			if probeErr := probeLaunchTokenUnderOwnershipLock(dataDir, sessionID, token); probeErr == nil {
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
	if afterValidation != nil {
		afterValidation()
	}
	descriptor, _ := descriptorPath(dataDir, sessionID)
	if err := os.Remove(descriptor); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retire prior-boot chat host descriptor: %w", err)
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retire prior-boot chat host lock: %w", err)
	}
	for token := range tokens {
		clearLaunchIntentUnlocked(dataDir, sessionID, token)
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
		if token, ok := launchTokenFromName(entry.Name()); ok {
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
		provenProviderTreeAbsence,
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
		provenProviderTreeAbsence,
	)
}

func probeLaunchTokenWithProbes(
	dataDir, sessionID, token string,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
	treeAbsent func(launchProvider) error,
) error {
	return probeLaunchTokenWithProbesAndClear(
		dataDir, sessionID, token,
		exited, bootIdentity, providerTreeDies, treeAbsent,
		clearLaunchIntent,
	)
}

func probeLaunchTokenUnderOwnershipLock(dataDir, sessionID, token string) error {
	return probeLaunchTokenWithProbesAndClear(
		dataDir, sessionID, token,
		processalive.Exited, currentBootIdentity, providerTreeDiesWithHost(), provenProviderTreeAbsence,
		clearLaunchIntentUnlocked,
	)
}

func probeLaunchTokenWithProbesAndClear(
	dataDir, sessionID, token string,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
	treeAbsent func(launchProvider) error,
	clearIntent func(string, string, string),
) error {
	intent, intentErr := readLaunchIntent(dataDir, sessionID, token)
	ready, readyErr := readLaunchReady(dataDir, sessionID, token)
	if intentErr != nil && !errors.Is(intentErr, os.ErrNotExist) {
		return fmt.Errorf("%w: read chat host launch intent: %w", ErrOwnershipInconclusive, intentErr)
	}
	if readyErr != nil && !errors.Is(readyErr, os.ErrNotExist) {
		return fmt.Errorf("%w: read chat host launch readiness: %w", ErrOwnershipInconclusive, readyErr)
	}
	settle := func(ready launchReady) error {
		return settleAdmittedHostOwnership(
			dataDir, sessionID, token, ready,
			exited, bootIdentity, providerTreeDies, treeAbsent, clearIntent,
		)
	}
	if readyErr == nil {
		return settle(ready)
	}
	if intentErr == nil {
		currentBootID, bootErr := bootIdentity()
		if bootErr == nil && priorBootIdentity(intent.BootID, currentBootID) {
			clearIntent(dataDir, sessionID, token)
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
			return settle(ready)
		}
		if !errors.Is(readyErr, os.ErrNotExist) {
			return fmt.Errorf("%w: re-read chat host launch readiness: %w", ErrOwnershipInconclusive, readyErr)
		}
		clearIntent(dataDir, sessionID, token)
		return nil
	}
	// Neither intent nor readiness exists. A provider record left by an
	// interrupted clear proves nothing on its own; sweep it.
	if path, err := launchProviderPath(dataDir, sessionID, token); err == nil {
		if _, statErr := os.Lstat(path); statErr == nil {
			clearIntent(dataDir, sessionID, token)
		}
	}
	return nil
}

func settleAdmittedHostOwnership(
	dataDir, sessionID, token string,
	ready launchReady,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
	treeAbsent func(launchProvider) error,
	clearIntent func(string, string, string),
) error {
	providerTreeAbsent := func() error {
		return recordedProviderTreeAbsence(dataDir, sessionID, token, ready, treeAbsent)
	}
	if err := admittedHostOwnershipError(ready, exited, bootIdentity, providerTreeDies, providerTreeAbsent); err != nil {
		return err
	}
	clearIntent(dataDir, sessionID, token)
	return nil
}

// recordedProviderTreeAbsence checks the provider record of an exited host
// and asks treeAbsent to prove that the tree it describes is gone.
func recordedProviderTreeAbsence(
	dataDir, sessionID, token string,
	ready launchReady,
	treeAbsent func(launchProvider) error,
) error {
	provider, err := readLaunchProvider(dataDir, sessionID, token)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("no provider process tree was recorded")
	}
	if err != nil {
		return fmt.Errorf("read recorded provider process tree: %w", err)
	}
	if provider.HostPID != ready.HostPID || provider.BootID != ready.BootID {
		return errors.New("recorded provider process tree belongs to another host launch")
	}
	return treeAbsent(provider)
}

// provenProviderTreeAbsence proves that no process of a dead host's provider
// tree is left. The provider (its group leader) must have exited and its
// process group must be empty. Then no live process of this user may:
//   - remain in the host's session, which holds every descendant that only
//     changed process group;
//   - carry the tree marker, which follows descendants that started a session
//     of their own (macOS hides the environment of system binaries such as
//     /bin/bash, so this alone misses Claude Code's detached shell there);
//   - work inside the session's worktree, which catches any command still able
//     to change it, wherever it moved.
//
// The scan runs twice so a member that forks while the first scan passes it
// is still seen. A reused PID or PGID, or an unrelated process in the
// worktree, can only keep the fence in place, never lift it.
func provenProviderTreeAbsence(provider launchProvider) error {
	if !processalive.Exited(provider.ProviderPGID) || !processGroupExited(provider.ProviderPGID) {
		return fmt.Errorf("provider process group %d is not proven absent", provider.ProviderPGID)
	}
	for range 2 {
		pid, err := providerTreeMember(provider.HostPID, provider.TreeMarker, provider.Workdir)
		if err != nil {
			return fmt.Errorf("look for surviving provider processes: %w", err)
		}
		if pid != 0 {
			return fmt.Errorf("provider process %d is still running", pid)
		}
	}
	return nil
}

func admittedHostOwnershipError(
	ready launchReady,
	exited func(int) bool,
	bootIdentity func() (string, error),
	providerTreeDies bool,
	providerTreeAbsent func() error,
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
	// same boot, orderly teardown proves cleanup by clearing the readiness
	// record; after an abrupt exit only the recorded, now-empty provider group
	// does.
	if absenceErr := providerTreeAbsent(); absenceErr != nil {
		return fmt.Errorf("%w: admitted chat host %d exited on the current boot without proving provider-tree cleanup: %w", ErrOwnershipInconclusive, ready.HostPID, absenceErr)
	}
	return nil
}

func priorBootIdentity(stored, current string) bool {
	storedSource, storedValue, storedOK := strings.Cut(stored, ":")
	currentSource, currentValue, currentOK := strings.Cut(current, ":")
	return storedOK && currentOK &&
		storedSource == currentSource &&
		storedValue != "" && currentValue != "" &&
		stored != current
}
