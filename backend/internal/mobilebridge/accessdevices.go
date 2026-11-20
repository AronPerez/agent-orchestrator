package mobilebridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// AccessDevice is a paired device. Only its token's SHA-256 is stored.
type AccessDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Platform  string `json:"platform,omitempty"`
	TokenHash string `json:"tokenHash"`
	// Scopes is empty for the existing full app credential. A non-empty list
	// is enforced by the LAN listener as a capability allowlist.
	Scopes []string `json:"scopes,omitempty"`
	// Issuer names the full credential that minted a scoped token. It lets
	// revoking that credential also revoke the authority it delegated.
	Issuer string `json:"issuer,omitempty"`
	// ClientID is the stable originating AO installation for idempotent scoped
	// issuance. Reissuing for the same scopes and client replaces the prior row,
	// including after a lost response followed by a parent credential change.
	ClientID string `json:"clientId,omitempty"`
	// InstallID links the device to its push registration.
	InstallID  string    `json:"installId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// Keys of the shared credentials' last-used times.
const (
	SharedPassword = "password"
	SharedAccount  = "account"
)

const (
	// DeviceTokenPrefix keeps a full app device token from looking like an account token.
	DeviceTokenPrefix = "aod_"
	// CoordinationTokenPrefix makes route-filtered coordination credentials
	// fail closed on older daemons, which recognize only DeviceTokenPrefix.
	CoordinationTokenPrefix = "aoc_"
	// DeviceIssuerPrefix distinguishes a full device credential from shared
	// password and account issuers in persisted scoped-token provenance.
	DeviceIssuerPrefix = "device:"
)

// DeviceScopeAgentCoordination filters direct HTTP calls to the reads and
// writes needed to list, spawn and message sessions on this host. Because those
// agents run with the user's OS authority, this is not an authority sandbox.
const DeviceScopeAgentCoordination = "agent-coordination"

// DeviceScopeDecisionSync limits a peer machine to the two decision ledger
// sync routes. It is minted by this host for one peer host id (ClientID).
const DeviceScopeDecisionSync = "decision-sync"

// maxAccessDevices bounds the device and capability tokens a full credential can mint.
const maxAccessDevices = 64

// touchInterval limits how often last-seen times are written to disk.
const touchInterval = 5 * time.Minute

// ErrTooManyDevices is returned by Issue when the registry is full.
var ErrTooManyDevices = errors.New("too many paired devices; revoke one first")

// ErrInvalidDeviceScopes keeps a caller of IssueScoped from accidentally
// minting a full credential after blank scopes normalize away.
var ErrInvalidDeviceScopes = errors.New("scoped device token requires at least one scope")

type accessDevicesFile struct {
	Devices        []AccessDevice       `json:"devices"`
	SharedLastUsed map[string]time.Time `json:"sharedLastUsed,omitempty"`
}

// AccessDevicesPath returns the registry's path in dataDir.
func AccessDevicesPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "access-devices.json")
}

// AccessDevices is the registry of device tokens the remote listener accepts.
type AccessDevices struct {
	// live holds one barrier per device: WhileLive holds a device's barrier
	// for reading around one bounded write, and removing that device holds
	// it for writing, so a removal returns only after every write that
	// device had admitted. Other devices and new pairings never wait on it.
	// Barriers are taken before mu; liveMu guards only the map. A barrier is
	// never deleted: a write may still hold or wait on it, and a removal
	// must always find that same barrier.
	liveMu sync.Mutex
	live   map[string]*sync.RWMutex
	mu     sync.RWMutex
	path   string
	// writeErr refuses every write when a damaged file could not be moved aside.
	writeErr error
	devices  map[string]AccessDevice
	byHash   map[string]string
	shared   map[string]time.Time
}

// LoadAccessDevices reads the registry. A damaged or unreadable file is moved
// aside and reported, and the registry starts empty.
func LoadAccessDevices(path string) (*AccessDevices, error) {
	r := &AccessDevices{path: path, devices: map[string]AccessDevice{}, byHash: map[string]string{}, shared: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		err = r.moveAside(fmt.Errorf("read access devices: %w", err))
		return r, err
	}
	var file accessDevicesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		err = r.moveAside(fmt.Errorf("parse access devices: %w", err))
		return r, err
	}
	for _, device := range file.Devices {
		if device.ID == "" || device.TokenHash == "" {
			continue
		}
		r.devices[device.ID] = device
		r.byHash[device.TokenHash] = device.ID
	}
	for kind, at := range file.SharedLastUsed {
		r.shared[kind] = at
	}
	return r, nil
}

func (r *AccessDevices) moveAside(cause error) error {
	if err := os.Rename(r.path, r.path+".bad"); err != nil {
		r.writeErr = fmt.Errorf("%w; moving it aside failed: %w", cause, err)
		return r.writeErr
	}
	return cause
}

// Issue registers a new device and returns it with its token.
func (r *AccessDevices) Issue(name, platform string, now time.Time) (AccessDevice, string, error) {
	device, token, _, err := r.issue(name, platform, nil, "", "", now)
	return device, token, err
}

// IssueScoped registers a new device token carrying the given capability
// scopes. Full app credentials are issued only through Issue.
func (r *AccessDevices) IssueScoped(name, platform string, scopes []string, now time.Time) (AccessDevice, string, error) {
	return r.IssueScopedBy(name, platform, scopes, "", now)
}

// IssueScopedBy registers a scoped token and records the full credential that
// delegated its authority. An empty issuer is accepted for callers that do not
// have an authentication context; network issuance always supplies one.
func (r *AccessDevices) IssueScopedBy(name, platform string, scopes []string, issuer string, now time.Time) (AccessDevice, string, error) {
	device, token, _, err := r.IssueScopedByClient(name, platform, scopes, issuer, "", now)
	return device, token, err
}

// IssueScopedByClient registers a scoped token for one stable originating
// client. A retry atomically replaces the prior token for the same exact scopes
// and client ID, even if the full parent credential changed after a lost
// response, so the old delegated authority cannot leak rows.
// Empty client IDs preserve the legacy append-only behavior.
func (r *AccessDevices) IssueScopedByClient(name, platform string, scopes []string, issuer, clientID string, now time.Time) (AccessDevice, string, []AccessDevice, error) {
	normalizedScopes := normalizeDeviceScopes(scopes)
	if len(normalizedScopes) == 0 {
		return AccessDevice{}, "", nil, ErrInvalidDeviceScopes
	}
	return r.issue(name, platform, normalizedScopes, strings.TrimSpace(issuer), cleanLabel(clientID, 128), now)
}

func (r *AccessDevices) issue(name, platform string, normalizedScopes []string, issuer, clientID string, now time.Time) (AccessDevice, string, []AccessDevice, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return AccessDevice{}, "", nil, err
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return AccessDevice{}, "", nil, err
	}
	prefix := DeviceTokenPrefix
	if len(normalizedScopes) > 0 {
		prefix = CoordinationTokenPrefix
	}
	token := prefix + hex.EncodeToString(secret)
	device := AccessDevice{
		ID:         "dev_" + hex.EncodeToString(id),
		Name:       cleanLabel(name, 64),
		Platform:   cleanLabel(strings.ToLower(platform), 32),
		TokenHash:  HashPassword(token),
		Scopes:     normalizedScopes,
		Issuer:     issuer,
		ClientID:   clientID,
		CreatedAt:  now.UTC(),
		LastSeenAt: now.UTC(),
	}
	// Reissuing for a client replaces, and so removes, its earlier device.
	if clientID != "" {
		defer r.holdRemoval(r.deviceIDsWhere(func(d AccessDevice) bool { return d.ClientID == clientID }))()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	replaced := make([]AccessDevice, 0, 1)
	if clientID != "" {
		for _, candidate := range r.devices {
			if candidate.ClientID == clientID && deviceScopesEqual(candidate.Scopes, normalizedScopes) {
				replaced = append(replaced, candidate)
			}
		}
	}
	if len(r.devices)-len(replaced) >= maxAccessDevices {
		return AccessDevice{}, "", nil, ErrTooManyDevices
	}
	for _, previous := range replaced {
		delete(r.devices, previous.ID)
		delete(r.byHash, previous.TokenHash)
	}
	r.devices[device.ID] = device
	r.byHash[device.TokenHash] = device.ID
	if err := r.persistLocked(); err != nil {
		delete(r.devices, device.ID)
		delete(r.byHash, device.TokenHash)
		for _, previous := range replaced {
			r.devices[previous.ID] = previous
			r.byHash[previous.TokenHash] = previous.ID
		}
		return AccessDevice{}, "", nil, err
	}
	for i := range replaced {
		replaced[i] = cloneAccessDevice(replaced[i])
	}
	return cloneAccessDevice(device), token, replaced, nil
}

func deviceScopesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// DeviceIssuer names the full device credential that delegated a scoped token.
func DeviceIssuer(deviceID string) string {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return ""
	}
	return DeviceIssuerPrefix + deviceID
}

// Match returns the device a token belongs to.
func (r *AccessDevices) Match(token string) (AccessDevice, bool) {
	coordination := strings.HasPrefix(token, CoordinationTokenPrefix)
	deviceToken := strings.HasPrefix(token, DeviceTokenPrefix)
	if !coordination && !deviceToken {
		return AccessDevice{}, false
	}
	hash := HashPassword(token)
	r.mu.RLock()
	defer r.mu.RUnlock()
	device, ok := r.devices[r.byHash[hash]]
	if !ok || (coordination && len(device.Scopes) == 0) || (deviceToken && len(device.Scopes) != 0) {
		return AccessDevice{}, false
	}
	return cloneAccessDevice(device), true
}

// WhileLive runs fn only while device id exists, and keeps the device from
// being removed until fn returns. It reports false, without calling fn, for
// a device that is gone. fn must be a short local write: never a network
// read or a wait, since every removal waits for it.
func (r *AccessDevices) WhileLive(id string, fn func() error) (bool, error) {
	if r == nil {
		return false, nil
	}
	barrier := r.barrier(id)
	barrier.RLock()
	defer barrier.RUnlock()
	if !r.Has(id) {
		return false, nil
	}
	return true, fn()
}

func (r *AccessDevices) barrier(id string) *sync.RWMutex {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	if r.live == nil {
		r.live = map[string]*sync.RWMutex{}
	}
	b := r.live[id]
	if b == nil {
		b = &sync.RWMutex{}
		r.live[id] = b
	}
	return b
}

// holdRemoval waits for the admitted writes of every device in ids and keeps
// new ones out until the returned release runs. Call it before taking mu.
func (r *AccessDevices) holdRemoval(ids []string) func() {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	held := make([]*sync.RWMutex, len(ids))
	for i, id := range ids {
		held[i] = r.barrier(id)
		held[i].Lock()
	}
	return func() {
		for _, b := range held {
			b.Unlock()
		}
	}
}

// deviceIDsWhere lists the devices that match.
func (r *AccessDevices) deviceIDsWhere(match func(AccessDevice) bool) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for id, d := range r.devices {
		if match(d) {
			ids = append(ids, id)
		}
	}
	return ids
}

// Has reports whether a device with id is still registered (not revoked).
func (r *AccessDevices) Has(id string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.devices[id]
	return ok
}

// Touch updates the device's last-seen time.
func (r *AccessDevices) Touch(id string, now time.Time) {
	r.mu.RLock()
	device, ok := r.devices[id]
	r.mu.RUnlock()
	if !ok || now.Sub(device.LastSeenAt) < touchInterval {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if device, ok = r.devices[id]; !ok {
		return
	}
	device.LastSeenAt = now.UTC()
	r.devices[id] = device
	_ = r.persistLocked()
}

// TouchShared updates a shared credential's last-used time.
func (r *AccessDevices) TouchShared(kind string, now time.Time) {
	r.mu.RLock()
	last := r.shared[kind]
	r.mu.RUnlock()
	if now.Sub(last) < touchInterval {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shared[kind] = now.UTC()
	_ = r.persistLocked()
}

// SharedLastUsed returns when a shared credential last authenticated a request.
func (r *AccessDevices) SharedLastUsed(kind string) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.shared[kind]
}

// LinkInstall records the push-registry row of the phone holding this token.
func (r *AccessDevices) LinkInstall(id, installID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[id]
	if !ok || device.InstallID == installID {
		return nil
	}
	previous := device.InstallID
	device.InstallID = installID
	r.devices[id] = device
	if err := r.persistLocked(); err != nil {
		device.InstallID = previous
		r.devices[id] = device
		return err
	}
	return nil
}

// Revoke removes a device and reports whether it existed.
func (r *AccessDevices) Revoke(id string) (AccessDevice, bool, error) {
	defer r.holdRemoval([]string{id})()
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[id]
	if !ok {
		return AccessDevice{}, false, nil
	}
	delete(r.devices, id)
	delete(r.byHash, device.TokenHash)
	if err := r.persistLocked(); err != nil {
		r.devices[id] = device
		r.byHash[device.TokenHash] = id
		return AccessDevice{}, true, err
	}
	return device, true, nil
}

// RevokeIssuedBy atomically removes every scoped token delegated by issuer.
// The removed records are returned so the caller can close live connections.
func (r *AccessDevices) RevokeIssuedBy(issuer string) ([]AccessDevice, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return nil, nil
	}
	defer r.holdRemoval(r.deviceIDsWhere(func(d AccessDevice) bool { return d.Issuer == issuer }))()
	r.mu.Lock()
	defer r.mu.Unlock()
	revoked := make([]AccessDevice, 0)
	for id, device := range r.devices {
		if device.Issuer != issuer {
			continue
		}
		revoked = append(revoked, device)
		delete(r.devices, id)
		delete(r.byHash, device.TokenHash)
	}
	if len(revoked) == 0 {
		return nil, nil
	}
	if err := r.persistLocked(); err != nil {
		for _, device := range revoked {
			r.devices[device.ID] = device
			r.byHash[device.TokenHash] = device.ID
		}
		return nil, err
	}
	sort.Slice(revoked, func(i, j int) bool {
		if !revoked[i].CreatedAt.Equal(revoked[j].CreatedAt) {
			return revoked[i].CreatedAt.Before(revoked[j].CreatedAt)
		}
		return revoked[i].ID < revoked[j].ID
	})
	for i := range revoked {
		revoked[i] = cloneAccessDevice(revoked[i])
	}
	return revoked, nil
}

// RevokeAll removes every device.
func (r *AccessDevices) RevokeAll() error {
	defer r.holdRemoval(r.deviceIDsWhere(func(AccessDevice) bool { return true }))()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.devices) == 0 {
		return nil
	}
	devices, byHash := r.devices, r.byHash
	r.devices, r.byHash = map[string]AccessDevice{}, map[string]string{}
	if err := r.persistLocked(); err != nil {
		r.devices, r.byHash = devices, byHash
		return err
	}
	return nil
}

// List returns the devices, oldest first.
func (r *AccessDevices) List() []AccessDevice {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AccessDevice, 0, len(r.devices))
	for _, device := range r.devices {
		out = append(out, cloneAccessDevice(device))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizeDeviceScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope = strings.TrimSpace(scope); scope != "" {
			seen[scope] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

func cloneAccessDevice(device AccessDevice) AccessDevice {
	device.Scopes = append([]string(nil), device.Scopes...)
	return device
}

func (r *AccessDevices) persistLocked() error {
	if r.writeErr != nil {
		return r.writeErr
	}
	file := accessDevicesFile{Devices: make([]AccessDevice, 0, len(r.devices)), SharedLastUsed: r.shared}
	for _, device := range r.devices {
		file.Devices = append(file.Devices, device)
	}
	sort.Slice(file.Devices, func(i, j int) bool { return file.Devices[i].ID < file.Devices[j].ID })
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(r.path, raw)
}

func cleanLabel(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, value))
	runes := []rune(value)
	for len(string(runes)) > limit {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimSpace(string(runes))
}
