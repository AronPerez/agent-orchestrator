package decisionsync_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

// fakePeer is a peer's remote listener: identity, mint and self-revoke.
type fakePeer struct {
	t        *testing.T
	hostID   string
	server   *httptest.Server
	mu       sync.Mutex
	bearers  []string
	revoked  []string
	mintCode int    // non-zero: refuse minting with this status
	mintEcho string // echoed in a refusal, like a careless peer might
}

func newFakePeer(t *testing.T, hostID string) *fakePeer {
	p := &fakePeer{t: t, hostID: hostID}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakePeer) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.bearers = append(p.bearers, r.Header.Get("Authorization"))
	p.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/identity":
		_ = json.NewEncoder(w).Encode(map[string]any{"hostId": p.hostID, "apiVersion": 1})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/remote-host/decision-sync-token":
		if p.mintCode != 0 {
			w.WriteHeader(p.mintCode)
			_, _ = io.WriteString(w, `{"error":"unauthorized","code":"BAD_PASSWORD","message":"wrong: `+p.mintEcho+`"}`)
			return
		}
		var in decisionsync.MintRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(decisionsync.MintResult{HostID: p.hostID, DeviceID: "dev_minted", Token: "aoc_minted_for_" + in.ClientID, Scope: "decision-sync"})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/remote-host/decision-sync-token/"):
		p.mu.Lock()
		p.revoked = append(p.revoked, strings.TrimPrefix(r.URL.Path, "/api/v1/remote-host/decision-sync-token/"))
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// memPeerStore is the status table in memory.
type memPeerStore struct {
	mu    sync.Mutex
	peers map[string]domain.DecisionSyncPeer
}

func newMemPeerStore() *memPeerStore {
	return &memPeerStore{peers: map[string]domain.DecisionSyncPeer{}}
}

func (s *memPeerStore) UpsertDecisionSyncPeer(_ context.Context, p domain.DecisionSyncPeer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[p.HostID] = p
	return nil
}

func (s *memPeerStore) DeleteDecisionSyncPeer(_ context.Context, hostID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.peers[hostID]
	delete(s.peers, hostID)
	return ok, nil
}

func (s *memPeerStore) RecordDecisionSyncAttempt(_ context.Context, hostID string, a domain.DecisionSyncAttempt) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[hostID]
	if !ok {
		return false, nil
	}
	p.LastAttemptAt, p.LastErrorCode = a.At, a.ErrorCode
	if a.ErrorCode == "" {
		p.LastSuccessAt, p.InSync, p.PeerVector, p.ClockAheadMS, p.PeerProtocol = a.At, a.InSync, a.PeerVector, a.ClockAheadMS, a.PeerProtocol
		if a.PeerLabel != "" {
			p.Label = a.PeerLabel
		}
	} else {
		p.InSync = false
	}
	s.peers[hostID] = p
	return true, nil
}

func (s *memPeerStore) get(hostID string) domain.DecisionSyncPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peers[hostID]
}

func (s *memPeerStore) ListDecisionSyncPeers(context.Context) ([]domain.DecisionSyncPeer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.DecisionSyncPeer
	for _, p := range s.peers {
		out = append(out, p)
	}
	return out, nil
}

func (s *memPeerStore) DecisionOriginsLastHeard(context.Context) (map[string]time.Time, error) {
	return map[string]time.Time{}, nil
}

func (s *memPeerStore) DecisionOriginLabels(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (s *memPeerStore) ListDecisionSyncConflicts(context.Context) ([]domain.DecisionSyncConflict, error) {
	return nil, nil
}

type linkFixture struct {
	peers   *decisionsync.Peers
	store   *memPeerStore
	secrets *decisionsync.PeerSecrets
	path    string
	changes int
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	path := decisionsync.PeerSecretsPath(t.TempDir())
	secrets, err := decisionsync.LoadPeerSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &linkFixture{store: newMemPeerStore(), secrets: secrets, path: path}
	f.peers = decisionsync.NewPeers(decisionsync.PeersDeps{
		Secrets: secrets, Store: f.store, API: decisionsync.NewHTTPClient(nil),
		HostID: "h_laptop", MachineLabel: func() string { return "laptop" }, Now: fixedNow(t0),
		OnChange: func() { f.changes++ },
	})
	return f
}

func TestLinkStoresOnlyTheScopedToken(t *testing.T) {
	peer := newFakePeer(t, "h_mini")
	f := newLinkFixture(t)
	const credential = "full-password-123"
	linked, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: peer.server.URL + "/", Label: "mac-mini", Credential: credential})
	if err != nil {
		t.Fatal(err)
	}
	if linked.HostID != "h_mini" || linked.Label != "mac-mini" || linked.Direction != domain.DecisionSyncOutbound || f.changes != 1 {
		t.Fatalf("linked = %+v, changes %d", linked, f.changes)
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), credential) || !strings.Contains(string(raw), "aoc_minted_for_h_laptop") {
		t.Fatalf("token file = %s", raw)
	}
	if row := f.store.get("h_mini"); row.URL != peer.server.URL || row.DeviceID != "dev_minted" {
		t.Fatalf("status row = %+v", row)
	}
	// The identity probe went out without any credential; only the mint carried one.
	if peer.bearers[0] != "" || peer.bearers[1] != "Bearer "+credential {
		t.Fatalf("bearers = %q", peer.bearers)
	}
}

func TestLinkRefusesSelfAndIdentityMismatch(t *testing.T) {
	self := newFakePeer(t, "h_laptop")
	f := newLinkFixture(t)
	if _, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: self.server.URL, Credential: "pw"}); !errors.Is(err, decisionsync.ErrLinkSelf) {
		t.Fatalf("self link err = %v", err)
	}
	other := newFakePeer(t, "h_other")
	if _, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: other.server.URL, ExpectedHostID: "h_mini", Credential: "pw"}); !errors.Is(err, decisionsync.ErrLinkHostChanged) {
		t.Fatalf("mismatch err = %v", err)
	}
	for _, p := range []*fakePeer{self, other} {
		for _, b := range p.bearers {
			if b != "" {
				t.Fatalf("a refused link sent a credential: %q", p.bearers)
			}
		}
	}
	if len(f.secrets.List()) != 0 || f.changes != 0 {
		t.Fatal("a refused link stored something")
	}
}

func TestUnlinkKeepsEntriesAndRevokesBestEffort(t *testing.T) {
	peer := newFakePeer(t, "h_mini")
	f := newLinkFixture(t)
	if _, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: peer.server.URL, Credential: "pw"}); err != nil {
		t.Fatal(err)
	}
	if err := f.peers.Unlink(context.Background(), "h_mini"); err != nil {
		t.Fatal(err)
	}
	if len(peer.revoked) != 1 || peer.revoked[0] != "dev_minted" || peer.bearers[len(peer.bearers)-1] != "Bearer aoc_minted_for_h_laptop" {
		t.Fatalf("revoked = %v, bearers = %q", peer.revoked, peer.bearers)
	}
	if len(f.secrets.List()) != 0 || f.store.get("h_mini").HostID != "" {
		t.Fatal("unlink left the token or the row")
	}

	// The peer is gone: unlinking still succeeds locally.
	gone := newFakePeer(t, "h_gone")
	if _, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: gone.server.URL, Credential: "pw"}); err != nil {
		t.Fatal(err)
	}
	gone.server.Close()
	ledger := newMachine("h_laptop", fixedNow(t0)).ledger
	ledger.Append(domainFuture(), t0)
	if err := f.peers.Unlink(context.Background(), "h_gone"); err != nil {
		t.Fatalf("unlink with the peer down: %v", err)
	}
	if len(ledger.Entries()) != 1 {
		t.Fatal("unlink touched ledger entries")
	}
	if err := f.peers.Unlink(context.Background(), "h_gone"); !errors.Is(err, decisionsync.ErrPeerNotLinked) {
		t.Fatalf("second unlink err = %v", err)
	}
}

func TestLinkErrorsNeverEchoCredential(t *testing.T) {
	const credential = "s3cret-full-credential"
	peer := newFakePeer(t, "h_mini")
	peer.mintCode = http.StatusUnauthorized
	peer.mintEcho = credential
	f := newLinkFixture(t)
	_, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: peer.server.URL, Credential: credential})
	if !errors.Is(err, decisionsync.ErrLinkCredentialBad) {
		t.Fatalf("err = %v, want ErrLinkCredentialBad", err)
	}
	if strings.Contains(err.Error(), credential) {
		t.Fatalf("error echoes the credential: %v", err)
	}
	for _, bad := range []string{"http://user:" + credential + "@host:3011", "http://host:3011/?token=" + credential} {
		_, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: bad, Credential: credential})
		if err == nil || strings.Contains(err.Error(), credential) {
			t.Fatalf("url %q: err = %v", bad, err)
		}
	}
}

func domainFuture() domain.DecisionEntry {
	e := domain.DecisionEntry{ID: "dent_local", CallID: "dec_1", Kind: "future_kind", Payload: json.RawMessage(`{}`), AuthorKind: domain.DecisionAuthorPerson}
	return e
}
