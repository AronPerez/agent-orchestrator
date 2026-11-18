package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// A remote target replaces the local run-file handshake: instead of reading
// running.json and gating on a live local PID, every daemon call goes to the
// given base URL carrying the daemon's connection password as a Bearer token —
// the same credential channel the mobile client uses (ADR 0001).
//
// It is opt-in and never inferred: with no --url and no AO_URL, the CLI keeps
// its loopback-only behavior exactly as before.
type remoteTarget struct {
	baseURL string
	token   string
	// label and hostID are populated only for a saved --host target, where they
	// name the destination in output and copyable commands.
	label  string
	hostID string
	// expectedHostID is the saved identity for --host or the probed identity for
	// --url/AO_URL. It is rechecked immediately before bearer use and sent as
	// defense in depth for listeners that enforce it.
	expectedHostID string
	// identityPinRequired opts coordination actions into the identity contract.
	// Older raw-URL commands predate that route and retain their compatibility.
	identityPinRequired bool
	// Host-local caller IDs may cross this target only after its relationship to
	// the source daemon has been proved.
	sourceRelationKnown bool
	sameAsSource        bool
	// accountUserID binds a desktop-imported credential to the Cloud account
	// that owns it. Empty means a local pairing, which remains usable signed out.
	accountUserID string
	// source names where the URL came from ("--url" or "AO_URL") so an error can
	// tell the user which one is pointing them off-box.
	source string
}

// remotesFileName holds saved remote daemons under the AO home directory. It
// carries credentials, so it must be mode 0600.
const remotesFileName = "remotes.json"

var savedHostIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var commandArgumentPattern = regexp.MustCompile(`^[A-Za-z0-9._:@%/+=,-]+$`)

// remotePreflightError means the destination credential was not sent. Callers
// that normally treat transport failures as ambiguous may safely recommend a
// retry of the original request instead of receipt-only recovery.
type remotePreflightError struct{ cause error }

func (e remotePreflightError) Error() string { return e.cause.Error() }
func (e remotePreflightError) Unwrap() error { return e.cause }

// activeCloudAccountFile is written by the desktop beside remotes.json. It is
// the authorization boundary for account-owned hosts; telemetry state is not,
// because a failed handoff must never preserve access after sign-out/switch.
const activeCloudAccountFile = "cloud_active_user"

// remotesFileEnv carries the desktop's host-store path through its daemon to
// spawned agents. AO_DATA_DIR names daemon state, which is ~/.ao/data in a
// packaged launch while the desktop store is ~/.ao/remotes.json.
const remotesFileEnv = "AO_REMOTES_FILE"

// cloudAccountFileEnv identifies the exact desktop instance whose live Cloud
// session owns account-scoped saved hosts. Dev and packaged desktops may run at
// the same time while sharing the top-level remotes store.
const cloudAccountFileEnv = "AO_CLOUD_ACCOUNT_FILE"

// coordinationTokenPrefix is distinct from the full-device aod_ format so an
// older daemon cannot promote this credential when it ignores scope metadata.
const coordinationTokenPrefix = "aoc_"

// remotesFile mirrors the desktop app's multi-node store. Full connection
// passwords may be OS-keychain sealed and are retained here only for the
// legacy --url lookup. coordinationToken is intentionally readable by the CLI:
// remotes.json is owner-only, and the token is protected as full transitive
// host authority because it can prompt agents running with OS authority.
type remotesFile struct {
	Remotes []remoteEntry `json:"remotes"`
}

type remoteEntry struct {
	Label             string `json:"label,omitempty"`
	URL               string `json:"url"`
	Password          string `json:"password"`
	HostID            string `json:"hostId,omitempty"`
	AccountUserID     string `json:"accountUserId,omitempty"`
	CoordinationToken string `json:"coordinationToken,omitempty"`
	CleanupOnly       bool   `json:"cleanupOnly,omitempty"`
}

// resolveRemoteTarget resolves the remote daemon this invocation targets, or
// nil for the default local daemon. The URL comes from --url or AO_URL; the
// credential from AO_TOKEN or a matching entry in ~/.ao/remotes.json.
func resolveRemoteTarget(flagURL string) (*remoteTarget, error) {
	return resolveRemoteTargetForAccount(flagURL, "")
}

func resolveRemoteTargetForAccount(flagURL, activeAccount string) (*remoteTarget, error) {
	raw, source := strings.TrimSpace(flagURL), "--url"
	if raw == "" {
		raw, source = strings.TrimSpace(os.Getenv("AO_URL")), "AO_URL"
	}
	if raw == "" {
		return nil, nil
	}
	base, err := normalizeRemoteURL(raw)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(os.Getenv("AO_TOKEN"))
	accountUserID := ""
	if token == "" {
		entry, err := lookupRemoteEntryForAccount(base, activeAccount)
		if err != nil {
			return nil, err
		}
		path, _ := remotesFilePath()
		switch {
		case entry == nil:
			return nil, fmt.Errorf("no connection password for %s — set AO_TOKEN or add an entry for it to %s", base, path)
		case strings.TrimSpace(entry.Password) == "":
			return nil, fmt.Errorf("the entry for %s in %s has an empty password — set its password, or use AO_TOKEN", base, path)
		}
		token = strings.TrimSpace(entry.Password)
		accountUserID = strings.TrimSpace(entry.AccountUserID)
	}
	return &remoteTarget{baseURL: base, token: token, accountUserID: accountUserID, source: source}, nil
}

// resolveSavedHostTarget resolves one exact desktop-saved label or stable host
// ID. It deliberately ignores password and sealedPassword: --host may use only
// the route-filtered credential minted for agent coordination. The filter is
// defense in depth; the credential still carries full transitive host authority.
func resolveSavedHostTarget(name string) (*remoteTarget, error) {
	return resolveSavedHostTargetForAccount(name, "")
}

func resolveSavedHostTargetForAccount(name, activeAccount string) (*remoteTarget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("saved host name must not be empty")
	}
	entries, err := loadRemoteEntries()
	if err != nil {
		return nil, err
	}
	var hostMatches, labelMatches []remoteEntry
	for _, entry := range entries {
		if !remoteEntryVisibleToAccount(entry, activeAccount) {
			continue
		}
		switch {
		case strings.TrimSpace(entry.HostID) == name:
			hostMatches = append(hostMatches, entry)
		case strings.TrimSpace(entry.Label) == name:
			labelMatches = append(labelMatches, entry)
		}
	}
	matches := hostMatches
	if len(matches) == 0 {
		matches = labelMatches
	}
	path, _ := remotesFilePath()
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no saved host named %q in %s", name, path)
	case 1:
		// Continue below.
	default:
		return nil, fmt.Errorf("saved host %q is ambiguous in %s — give the host ID instead", name, path)
	}
	entry := matches[0]
	hostID := strings.TrimSpace(entry.HostID)
	if hostID == "" {
		return nil, fmt.Errorf("saved host %q has no host ID — reconnect it in the desktop app before using --host", name)
	}
	if !savedHostIDPattern.MatchString(hostID) {
		return nil, fmt.Errorf("saved host %q has an invalid host ID — reconnect it in the desktop app before using --host", name)
	}
	token := strings.TrimSpace(entry.CoordinationToken)
	if token == "" {
		return nil, fmt.Errorf("saved host %q has no agent coordination credential — connect it in the desktop app once, then retry", name)
	}
	if !strings.HasPrefix(token, coordinationTokenPrefix) {
		return nil, fmt.Errorf("saved host %q has an invalid coordination credential — reconnect it in the desktop app before using --host", name)
	}
	base, err := normalizeRemoteURL(entry.URL)
	if err != nil {
		return nil, fmt.Errorf("saved host %q: %w", name, err)
	}
	label := strings.TrimSpace(entry.Label)
	if label == "" {
		label = hostID
	}
	return &remoteTarget{
		baseURL: base, token: token, label: label, hostID: hostID, expectedHostID: hostID,
		accountUserID: strings.TrimSpace(entry.AccountUserID), source: "--host",
	}, nil
}

type savedHostIdentity struct {
	HostID     string `json:"hostId"`
	APIVersion int    `json:"apiVersion"`
}

const coordinationCapabilityAutomationAuthored = "automation-authored"

type coordinationCapabilitiesResponse struct {
	Capabilities []string `json:"capabilities"`
}

// selectSavedHost proves the saved address still answers for the saved host ID
// before placing its credential in commandContext. The identity endpoint is the
// sole unauthenticated LAN route; no bearer is present on this probe.
func (c *commandContext) selectSavedHost(ctx context.Context, name string) error {
	if c.remote != nil {
		return usageError{errors.New("--host cannot be combined with --url or AO_URL")}
	}
	activeAccount := c.activeCloudAccount(ctx)
	target, err := resolveSavedHostTargetForAccount(name, activeAccount)
	if err != nil {
		return err
	}
	if local := c.localDaemonHostID(ctx); local != "" && local == target.hostID {
		return fmt.Errorf("saved host %q is this AO installation itself", target.label)
	}
	actual, err := c.probeSavedHostIdentity(ctx, target.baseURL)
	if err != nil {
		return fmt.Errorf("verify saved host %q: %w", target.label, err)
	}
	if actual.HostID != target.hostID {
		return fmt.Errorf("saved host identity changed for %s: expected %s, got %s; connection refused", target.baseURL, target.hostID, actual.HostID)
	}
	if local := c.localDaemonHostID(ctx); local != "" && local == actual.HostID {
		return fmt.Errorf("saved host %q is this AO installation itself", target.label)
	}
	if target.accountUserID != "" && c.activeCloudAccount(ctx) != target.accountUserID {
		return errors.New("active Cloud account changed while verifying the saved host; retry with the intended account signed in")
	}
	c.remote = target
	return nil
}

// remoteRetryArguments preserves the destination without its bearer. Rendering
// is shell-specific and happens only after these values are structured.
func (c *commandContext) remoteRetryArguments() []string {
	if c.remote == nil {
		return nil
	}
	if c.remote.source == "--host" && savedHostIDPattern.MatchString(c.remote.hostID) {
		return []string{"--host", c.remote.hostID}
	}
	if c.remote.source == "--url" || c.remote.source == "AO_URL" {
		return []string{"--url", c.remote.baseURL}
	}
	return nil
}

func quoteCommandArgument(value string) string {
	if commandArgumentPattern.MatchString(value) && !strings.HasPrefix(value, "=") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

var activeCloudUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type activeCloudAccountMarker struct {
	UserID             string `json:"userId"`
	DesktopPID         int    `json:"desktopPid"`
	DesktopStartedAtMS int64  `json:"desktopStartedAtMs"`
}

// activeCloudAccount reads the desktop auth authority beside the exact remote
// store being resolved. The marker is valid only while its desktop process is
// alive, so a stale file after a crash cannot authorize account-owned hosts.
// Any read, parse, validation, or liveness failure hides those hosts; local
// pairings (which have no accountUserId) remain usable.
func (c *commandContext) activeCloudAccount(ctx context.Context) string {
	markerPath := strings.TrimSpace(os.Getenv(cloudAccountFileEnv))
	if markerPath == "" {
		remotesPath, err := remotesFilePath()
		if err != nil {
			return ""
		}
		markerPath = filepath.Join(filepath.Dir(remotesPath), activeCloudAccountFile)
	}
	raw, err := os.ReadFile(markerPath) // #nosec G304 -- desktop-carried owner-only authorization marker.
	if err != nil {
		return ""
	}
	var marker activeCloudAccountMarker
	if json.Unmarshal(raw, &marker) != nil || marker.DesktopPID <= 0 || marker.DesktopStartedAtMS <= 0 || !activeCloudUserIDPattern.MatchString(marker.UserID) {
		return ""
	}
	if c.deps.ProcessAlive == nil || !c.deps.ProcessAlive(marker.DesktopPID) {
		return ""
	}
	startedAt, ok := c.deps.ProcessStartedAt(ctx, marker.DesktopPID)
	if !ok || absDuration(startedAt.Sub(time.UnixMilli(marker.DesktopStartedAtMS))) > 5*time.Second {
		return ""
	}
	return marker.UserID
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

// selectSavedHostIfSet lets individual commands opt into saved-host targeting
// without making --host a global flag that unsupported commands could accept.
func (c *commandContext) selectSavedHostIfSet(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	return c.selectSavedHost(ctx, name)
}

// savedHostRetryArgs preserves the destination in a copyable retry command.
// Host IDs are stable and unambiguous even when a display label later changes.
func (c *commandContext) savedHostRetryArgs() string {
	if c.remote == nil || !savedHostIDPattern.MatchString(c.remote.hostID) {
		return ""
	}
	return " --host " + c.remote.hostID
}

func (c *commandContext) probeSavedHostIdentity(ctx context.Context, base string) (savedHostIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/identity", http.NoBody) // #nosec G704 -- base is an explicitly saved AO host.
	if err != nil {
		return savedHostIdentity{}, err
	}
	client := *c.deps.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req) // #nosec G704 -- target is an explicitly saved AO host, probed without credentials.
	if err != nil {
		return savedHostIdentity{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return savedHostIdentity{}, fmt.Errorf("identity probe returned HTTP %d", resp.StatusCode)
	}
	var identity savedHostIdentity
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&identity); err != nil {
		return savedHostIdentity{}, fmt.Errorf("decode identity probe: %w", err)
	}
	if strings.TrimSpace(identity.HostID) == "" || identity.HostID == "local" {
		return savedHostIdentity{}, errors.New("identity probe returned no valid host ID")
	}
	if identity.APIVersion != 1 {
		return savedHostIdentity{}, fmt.Errorf("remote coordination API version %d is incompatible with version 1", identity.APIVersion)
	}
	return identity, nil
}

// sourceDaemonHostID proves that the daemon selected by AO_RUN_FILE owns the
// ambient source session and matches the protected AO_DATA_DIR injected when
// that session launched. The final identity read catches replacement during
// the lookup; the data-root identity catches a run file repointed beforehand.
func (c *commandContext) sourceDaemonHostID(ctx context.Context, sessionID string) (string, error) {
	launchHostID := readLocalHostID()
	if launchHostID == "" {
		return "", errors.New("source session launch host identity is unavailable; retry from the session that started this command")
	}
	base, err := c.localDaemonBase()
	if err != nil {
		return "", fmt.Errorf("source session daemon is unavailable: %w", err)
	}
	before, err := c.probeSavedHostIdentity(ctx, base)
	if err != nil {
		return "", fmt.Errorf("identify source session daemon: %w", err)
	}
	if before.HostID != launchHostID {
		return "", fmt.Errorf("selected source daemon %s does not match this session's launch host %s; refusing host-local session attribution", before.HostID, launchHostID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/v1/sessions/"+url.PathEscape(sessionID), http.NoBody)
	if err != nil {
		return "", err
	}
	client := *c.deps.HTTPClient
	client.Timeout = probeTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req) // #nosec G704 -- base is the loopback daemon selected by the validated run file.
	if err != nil {
		return "", fmt.Errorf("verify source session daemon: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("source session %s is not owned by the selected local daemon (HTTP %d)", sessionID, resp.StatusCode)
	}
	var body struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("decode source session: %w", err)
	}
	if body.Session.ID != sessionID {
		return "", fmt.Errorf("source session daemon returned %q for %q", body.Session.ID, sessionID)
	}
	after, err := c.probeSavedHostIdentity(ctx, base)
	if err != nil {
		return "", fmt.Errorf("revalidate source session daemon: %w", err)
	}
	if after.HostID != before.HostID {
		return "", errors.New("source session daemon changed while resolving its host identity; retry")
	}
	return before.HostID, nil
}

func (c *commandContext) remoteRelationToSource(ctx context.Context, sessionID string) (sourceHostID string, sameHost bool, err error) {
	sourceHostID, err = c.sourceDaemonHostID(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	destinationHostID := c.remote.expectedHostID
	if destinationHostID == "" {
		destination, probeErr := c.probeSavedHostIdentity(ctx, c.remote.baseURL)
		if probeErr != nil {
			return "", false, fmt.Errorf("identify destination daemon: %w", probeErr)
		}
		destinationHostID = destination.HostID
		c.remote.expectedHostID = destinationHostID
	}
	c.remote.sourceRelationKnown = true
	c.remote.sameAsSource = destinationHostID == sourceHostID
	return sourceHostID, c.remote.sameAsSource, nil
}

func (c *commandContext) revalidateRemoteTarget(ctx context.Context) error {
	if c.remote == nil {
		return nil
	}
	if c.remote.expectedHostID == "" && !c.remote.identityPinRequired {
		return nil
	}
	identity, err := c.probeSavedHostIdentity(ctx, c.remote.baseURL)
	if err != nil {
		return remotePreflightError{cause: fmt.Errorf("revalidate remote host identity: %w", err)}
	}
	if c.remote.expectedHostID == "" {
		c.remote.expectedHostID = identity.HostID
	} else if identity.HostID != c.remote.expectedHostID {
		return remotePreflightError{cause: fmt.Errorf("remote host identity changed: expected %s, got %s; credential was not sent", c.remote.expectedHostID, identity.HostID)}
	}
	if c.remote.accountUserID != "" && c.activeCloudAccount(ctx) != c.remote.accountUserID {
		return remotePreflightError{cause: errors.New("active Cloud account changed before the remote request; credential was not sent")}
	}
	return nil
}

func (c *commandContext) requireRemoteCoordinationCapability(ctx context.Context, capability string) error {
	var response coordinationCapabilitiesResponse
	if err := c.getJSON(ctx, "coordination/capabilities", &response); err != nil {
		return fmt.Errorf("destination does not advertise %s support; upgrade Orchestrator on that machine before sending this agent-authored message: %w", capability, err)
	}
	for _, available := range response.Capabilities {
		if available == capability {
			return nil
		}
	}
	return fmt.Errorf("destination does not support %s; upgrade Orchestrator on that machine before sending this agent-authored message", capability)
}

func (c *commandContext) localDaemonHostID(ctx context.Context) string {
	base, err := c.localDaemonBase()
	if err == nil {
		if identity, probeErr := c.probeSavedHostIdentity(ctx, base); probeErr == nil {
			return identity.HostID
		}
	}
	return readLocalHostID()
}

// readLocalHostID is best-effort and read-only. A missing local daemon identity
// must not create one merely because the CLI is resolving a saved host.
func readLocalHostID() string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "mobile", "identity.json")) // #nosec G304 -- fixed path under AO's configured data directory.
	if err != nil {
		return ""
	}
	var identity struct {
		HostID string `json:"hostId"`
	}
	if json.Unmarshal(raw, &identity) != nil {
		return ""
	}
	return strings.TrimSpace(identity.HostID)
}

// normalizeRemoteURL turns a user-supplied target into a base URL with no
// trailing slash. A bare "host:3011" is accepted and assumed plaintext http,
// matching the LAN listener (ADR 0001 is deliberately http-only).
func normalizeRemoteURL(raw string) (string, error) {
	// A URL-embedded credential is never trusted — the connection password comes
	// from AO_TOKEN or remotes.json. Silently dropping it would leave the user
	// certain they had passed a password and then told there was none, so say so
	// instead. Checked first, and reported without quoting the URL, so no later
	// error path can echo the credential into a message or a log line.
	if hasUserinfo(raw) {
		return "", errors.New("invalid daemon URL: it must not carry a username or password — " +
			"pass the connection password in AO_TOKEN or a ~/.ao/remotes.json entry")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid daemon URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid daemon URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid daemon URL %q: missing host", raw)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

// hasUserinfo reports whether raw's authority component carries userinfo. It
// works textually, before url.Parse, so that a malformed URL cannot slip a
// credential into the parse error — and it catches the scheme-less form
// ("user:pw@host:3011") too. An "@" in the path or query is not authority.
func hasUserinfo(raw string) bool {
	authority := raw
	if i := strings.Index(authority, "://"); i >= 0 {
		authority = authority[i+3:]
	}
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority = authority[:i]
	}
	return strings.Contains(authority, "@")
}

func remotesFilePath() (string, error) {
	if raw := strings.TrimSpace(os.Getenv(remotesFileEnv)); raw != "" {
		return filepath.Abs(raw)
	}
	// The desktop places all of an isolated run's state directly under the
	// explicit AO_DATA_DIR. Mirror that choice so its CLI sees the same hosts.
	if raw := strings.TrimSpace(os.Getenv("AO_DATA_DIR")); raw != "" {
		dir, err := filepath.Abs(raw)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, remotesFileName), nil
	}
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, remotesFileName), nil
}

// loadRemoteEntries reads the desktop's saved hosts. A file readable by anyone
// but the owner is refused rather than used: it carries a plaintext
// full-transitive-authority coordination token even when the full connection
// password is keychain-sealed.
func loadRemoteEntries() ([]remoteEntry, error) {
	path, err := remotesFilePath()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// Windows file modes do not carry meaningful group/other bits, so the check
	// would reject every file there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s holds remote credentials and is readable by others (mode %04o) — run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed path under the AO home directory.
	if err != nil {
		return nil, err
	}
	var file remotesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file.Remotes, nil
}

func lookupRemoteEntryForAccount(base, activeAccount string) (*remoteEntry, error) {
	entries, err := loadRemoteEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !remoteEntryVisibleToAccount(entry, activeAccount) {
			continue
		}
		saved, err := normalizeRemoteURL(entry.URL)
		if err != nil {
			continue // a hand-edited entry must not break every other one
		}
		if saved == base {
			return &entry, nil
		}
	}
	return nil, nil
}

func remoteEntryVisibleToAccount(entry remoteEntry, activeAccount string) bool {
	if entry.CleanupOnly {
		return false
	}
	owner := strings.TrimSpace(entry.AccountUserID)
	return owner == "" || (strings.TrimSpace(activeAccount) != "" && owner == strings.TrimSpace(activeAccount))
}

func savedRemoteCredentialRequested(flagURL string) bool {
	if strings.TrimSpace(os.Getenv("AO_TOKEN")) != "" {
		return false
	}
	return strings.TrimSpace(flagURL) != "" || strings.TrimSpace(os.Getenv("AO_URL")) != ""
}

// resolvedBySuffix names the daemon that acted, for the messages where the
// operator can still catch a mistake. Empty for a local daemon, so local output
// stays byte-identical; a remote target is never silent.
//
// It is also what the destructive verbs use — `session kill`, `session cleanup`
// and `project rm` are correct about which daemon they hit, but a prompt that
// says "across all projects" and a success line that says "session x killed"
// name no host, and a session or project id is not host-qualified. For a
// destructive verb, "you are about to do this, and you cannot tell where" is the
// whole defect.
//
// It began as a path echo, because the path echo alone is not a signal: the daemon resolves
// the path against its OWN filesystem, and for an absolute path the echoed
// string is byte-identical to what the operator typed — so it carries no
// information about which machine resolved it. Measured: `ao project add
// --path '~/repo' --url <remote>` registers the REMOTE host's ~/repo, and the
// operator's own ~/repo is never consulted.
func (c *commandContext) resolvedBySuffix() string {
	if c.remote == nil {
		return ""
	}
	if c.remote.hostID != "" {
		return fmt.Sprintf(" on %s (%s)", c.remote.label, c.remote.hostID)
	}
	return " on the remote daemon at " + c.remote.baseURL
}

// checkRemoteProjectPath refuses a host-relative path aimed at a remote daemon.
//
// Refuse rather than warn: "~/repo" and "./repo" are resolved by the daemon
// against its own home and its own working directory, so against a remote
// target they silently name a directory on someone else's machine. There is no
// reading under which the operator meant the remote daemon's home — and unlike
// an absolute path, which may legitimately exist on either host, a host-relative
// path cannot be checked by the operator after the fact. A warning on stderr
// would be missed exactly when it matters, in a script or a busy terminal.
//
// Absolute paths are deliberately still allowed: they are meaningful on the
// remote host, and refusing them would make a remote target useless.
func (c *commandContext) checkRemoteProjectPath(path string) error {
	if c.remote == nil {
		return nil
	}
	trimmed := strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(trimmed, "~"):
		return usageError{fmt.Errorf(
			"--path %q is relative to a home directory, and %s targets the remote daemon at %s, "+
				"where it would mean that host's home — pass an absolute path as it exists on that host",
			path, c.remote.source, c.remote.baseURL)}
	case !isAbsForSomeHost(trimmed):
		return usageError{fmt.Errorf(
			"--path %q is relative, and %s targets the remote daemon at %s, where it would be resolved "+
				"against that daemon's working directory — pass an absolute path as it exists on that host",
			path, c.remote.source, c.remote.baseURL)}
	}
	return nil
}

// isAbsForSomeHost reports whether p is an absolute path on ANY host AO might
// talk to, rather than on the machine running the CLI.
//
// filepath.IsAbs is the wrong question here and shipping it was a bug: it
// judges by the local OS, so on Windows it calls "/srv/repo" relative — and a
// Windows operator targeting a Linux remote host, which is exactly what remote
// execution creates, was refused a perfectly valid path on that host. The
// destination filesystem is the remote daemon's, so the CLI must accept every
// absolute form and let the daemon judge its own.
//
// Deliberately NOT accepted, because both are host-relative even on Windows:
// a bare drive-relative path ("C:foo") and a single leading backslash
// ("\foo", relative to the current drive).
func isAbsForSomeHost(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true // POSIX absolute
	}
	if strings.HasPrefix(p, `\\`) {
		return true // Windows UNC: \\server\share
	}
	// Windows drive-absolute: C:\foo or C:/foo (a slash after the colon is
	// what separates this from drive-RELATIVE "C:foo").
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return (p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')
	}
	return false
}

// checkRemoteImplicitProject refuses to resolve a project from a local signal
// when the command targets a remote daemon.
//
// Refuse rather than guess, for the same reason as checkRemoteProjectPath:
// AO_PROJECT_ID, AO_SESSION_ID and the current directory all describe THIS
// machine, but with a remote target they are matched against the remote
// daemon's projects. Measured: `ao spawn --url <remote>` run inside any AO
// session inherits an AO_PROJECT_ID the operator never typed and spawns
// against whatever project on the remote host happens to share that id — and
// cwd matching picks a remote project whenever the two machines' paths
// coincide. A session started on the wrong machine cannot be caught after the
// fact from the output, which names only the session it created.
//
// So --project is required for a remote target: it is the one input that means
// the same thing on both hosts.
func (c *commandContext) checkRemoteImplicitProject(explicit string) error {
	if c.remote == nil || strings.TrimSpace(explicit) != "" {
		return nil
	}
	signal := "the current directory"
	switch {
	case strings.TrimSpace(os.Getenv("AO_PROJECT_ID")) != "":
		signal = "AO_PROJECT_ID"
	case strings.TrimSpace(os.Getenv("AO_SESSION_ID")) != "":
		signal = "AO_SESSION_ID"
	}
	if c.remote.hostID != "" {
		return usageError{fmt.Errorf(
			"%s describes this machine, but --host resolved saved machine %s (%s), where it could select a different project — "+
				"pass --project <id> as it exists on that machine",
			signal, c.remote.label, c.remote.hostID)}
	}
	return usageError{fmt.Errorf(
		"%s describes this machine, and %s targets the remote daemon at %s, where it would select "+
			"a project on that host — pass --project <id> as it exists on that host "+
			"(list them with `ao project ls --url %s`)",
		signal, c.remote.source, c.remote.baseURL, c.remote.baseURL)}
}

// refuseLocalOnly refuses a command that acts on the machine running the CLI
// and therefore cannot honour a remote target.
//
// These commands do not fail against --url: they succeed, on the wrong machine,
// and say nothing about it. `ao doctor --url` reports the laptop's git, tmux and
// data dir; `ao import --url` opens the laptop's database; `ao start --url`
// opens the laptop's desktop app. A command that acts on the wrong host is
// undetectable after the fact, which is the defect class #50 and #56 ship
// refusals for — so the message names the flag (--url or AO_URL), names the URL
// it points at, and says where to run the command instead. It never guesses at
// a remote equivalent.
//
// Exit code 2 (usage), matching #50 and #56: passing --url to a command that
// cannot use it is misuse of a flag, not a runtime failure.
func (c *commandContext) refuseLocalOnly(command, why string) error {
	if c.remote == nil {
		return nil
	}
	return usageError{fmt.Errorf("%s targets the remote daemon at %s, but `%s` %s",
		c.remote.source, c.remote.baseURL, command, why)}
}

// refuseDaemonURLFlag is refuseLocalOnly for `ao daemon`, narrowed to an
// explicit --url and deliberately ignoring AO_URL.
//
// The one asymmetry among the local-only refusals. An explicit --url is a
// keystroke and always misuse. AO_URL is an exported shell variable — the very
// foot-gun a remote-access guide creates — and `ao daemon` is spawned by the
// desktop app, not typed. Refusing it on AO_URL would take an operator's
// working remote setup and turn it into a dead desktop app on their own
// machine, which is worse than the ignored flag this refuses.
func (c *commandContext) refuseDaemonURLFlag() error {
	if c.remote == nil || c.remote.source != "--url" {
		return nil
	}
	return c.refuseLocalOnly("ao daemon",
		"runs a daemon process on the machine executing it and makes no outbound call — "+
			"there is nothing it could do with that URL. Start the daemon on that host")
}

// pinToLocalDaemon keeps a daemon-local callback on the local daemon.
//
// `ao hooks` and `ao agent-process supervise` are hidden commands an agent
// process fires at the daemon that started it: they report activity for a
// session that exists on THIS machine. Sending that to a remote daemon is never
// right — measured, it reports SESSION_NOT_FOUND and exits 0, so the local UI's
// activity feed simply goes dead while the agent keeps working.
//
// The split is the same one refuseDaemonURLFlag makes, for the same reason, and
// it is the whole point of this helper:
//
//   - AO_URL is ignored. These commands are spawned by the harness, not typed,
//     and an operator who exports AO_URL in a shell profile — the natural thing
//     to do after reading the remote-access guide — must not have every local
//     agent broken by it. Refusing here would break the user's agent, the exact
//     thing hooks are designed never to do. The ignore is logged to hooks.log so
//     it is discoverable rather than silent.
//   - An explicit --url is refused (exit 2). A typed flag on a hidden,
//     agent-invoked command is misuse, and nothing in AO constructs one:
//     session_manager builds the supervise argv itself, with fixed arguments.
func (c *commandContext) pinToLocalDaemon(command string) error {
	if c.remote == nil {
		return nil
	}
	if c.remote.source == "--url" {
		return c.refuseLocalOnly(command,
			"reports on a session owned by the daemon that started it, so it can only ever mean the local "+
				"daemon — there is no remote form. Drop the flag")
	}
	target := c.remote.baseURL
	c.remote = nil
	noteIgnoredRemoteTarget(command, target)
	return nil
}

// authorize presents the remote connection password. Loopback calls carry no
// credential: the local daemon's loopback listener has no auth at all.
func (c *commandContext) authorize(req *http.Request) {
	if c.remote != nil {
		req.Header.Set("Authorization", "Bearer "+c.remote.token)
		if c.remote.expectedHostID != "" {
			req.Header.Set("X-AO-Expected-Host-ID", c.remote.expectedHostID)
		}
	}
}
