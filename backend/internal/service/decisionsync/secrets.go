package decisionsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// StoredPeer is one outbound link and the scoped token the peer minted for
// this machine. It lives only in the owner-only token file: never in SQLite,
// API responses, logs, telemetry or the renderer.
type StoredPeer struct {
	HostID   string `json:"hostId"`
	URL      string `json:"url"`
	Label    string `json:"label,omitempty"`
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}

type peerSecretsFile struct {
	Peers []StoredPeer `json:"peers"`
}

// PeerSecretsPath returns the token file's path under the data directory.
func PeerSecretsPath(dataDir string) string {
	return filepath.Join(dataDir, "decision-sync", "peers.json")
}

// PeerSecrets is the owner-only file of linked peers and their tokens.
type PeerSecrets struct {
	mu    sync.Mutex
	path  string
	peers map[string]StoredPeer
}

// LoadPeerSecrets reads the token file. A damaged or unreadable file is moved
// aside to .bad and reported, and the registry starts empty, like the access
// device registry.
func LoadPeerSecrets(path string) (*PeerSecrets, error) {
	s := &PeerSecrets{path: path, peers: map[string]StoredPeer{}}
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed path under the AO data directory.
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	var file peerSecretsFile
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		if moveErr := os.Rename(path, path+".bad"); moveErr != nil {
			return s, fmt.Errorf("decision sync peers file is damaged and could not be moved aside: %w", moveErr)
		}
		return s, errors.New("decision sync peers file was damaged; moved aside to peers.json.bad and starting with no links")
	}
	for _, peer := range file.Peers {
		if peer.HostID != "" {
			s.peers[peer.HostID] = peer
		}
	}
	return s, nil
}

// List returns every stored peer by host id.
func (s *PeerSecrets) List() []StoredPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StoredPeer, 0, len(s.peers))
	for _, peer := range s.peers {
		out = append(out, peer)
	}
	slices.SortFunc(out, func(a, b StoredPeer) int { return strings.Compare(a.HostID, b.HostID) })
	return out
}

// Get returns one stored peer.
func (s *PeerSecrets) Get(hostID string) (StoredPeer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, ok := s.peers[hostID]
	return peer, ok
}

// Put stores or replaces a peer and writes the file.
func (s *PeerSecrets) Put(peer StoredPeer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, had := s.peers[peer.HostID]
	s.peers[peer.HostID] = peer
	if err := s.persistLocked(); err != nil {
		if had {
			s.peers[peer.HostID] = previous
		} else {
			delete(s.peers, peer.HostID)
		}
		return err
	}
	return nil
}

// Delete removes a peer and writes the file. It returns the removed peer.
func (s *PeerSecrets) Delete(hostID string) (StoredPeer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, ok := s.peers[hostID]
	if !ok {
		return StoredPeer{}, false, nil
	}
	delete(s.peers, hostID)
	if err := s.persistLocked(); err != nil {
		s.peers[hostID] = peer
		return StoredPeer{}, false, err
	}
	return peer, true, nil
}

// persistLocked writes the file atomically: a 0600 temporary file in the same
// 0700 directory, synced, then renamed over the old one.
func (s *PeerSecrets) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create decision sync directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a directory needs its execute bit; 0700 is owner-only.
		return fmt.Errorf("protect decision sync directory: %w", err)
	}
	file := peerSecretsFile{Peers: make([]StoredPeer, 0, len(s.peers))}
	for _, peer := range s.peers {
		file.Peers = append(file.Peers, peer)
	}
	slices.SortFunc(file.Peers, func(a, b StoredPeer) int { return strings.Compare(a.HostID, b.HostID) })
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".peers-*.json")
	if err != nil {
		return fmt.Errorf("write decision sync peers: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("protect decision sync peers: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write decision sync peers: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync decision sync peers: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close decision sync peers: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		cleanup()
		return fmt.Errorf("replace decision sync peers: %w", err)
	}
	return nil
}
