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
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
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
		p.mu.Lock()
		hostID := p.hostID
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"hostId": hostID, "apiVersion": 1})
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

func (s *memPeerStore) GetDecisionSyncPeer(_ context.Context, hostID string) (domain.DecisionSyncPeer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[hostID]
	return p, ok, nil
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
		if p.Label == "" {
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

func TestUnlinkProbesIdentityBeforeRevoking(t *testing.T) {
	peer := newFakePeer(t, "h_mini")
	f := newLinkFixture(t)
	if _, _, err := f.peers.Link(context.Background(), decisionsync.LinkInput{URL: peer.server.URL, Credential: "pw"}); err != nil {
		t.Fatal(err)
	}
	// The address now answers as another machine.
	peer.mu.Lock()
	peer.hostID = "h_stranger"
	peer.bearers = nil
	peer.mu.Unlock()
	if err := f.peers.Unlink(context.Background(), "h_mini"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	for _, b := range peer.bearers {
		if b != "" {
			t.Fatalf("unlink sent the token to a different machine: %q", peer.bearers)
		}
	}
	if len(peer.revoked) != 0 {
		t.Fatalf("revoked on the wrong machine: %v", peer.revoked)
	}
	if len(f.secrets.List()) != 0 {
		t.Fatal("unlink kept the token locally")
	}
}

func TestUnlinkRefusesInboundOnlyPeer(t *testing.T) {
	f := newLinkFixture(t)
	if err := f.store.UpsertDecisionSyncPeer(context.Background(), domain.DecisionSyncPeer{HostID: "h_laptop2", Label: "laptop-2", Direction: domain.DecisionSyncInbound}); err != nil {
		t.Fatal(err)
	}
	err := f.peers.Unlink(context.Background(), "h_laptop2")
	if !errors.Is(err, decisionsync.ErrPeerInboundOnly) {
		t.Fatalf("err = %v, want ErrPeerInboundOnly", err)
	}
	if f.store.get("h_laptop2").HostID == "" {
		t.Fatal("refused unlink still deleted the inbound peer's row")
	}
}

// failingUpsertStore fails to create status rows.
type failingUpsertStore struct{ *memPeerStore }

func (failingUpsertStore) UpsertDecisionSyncPeer(context.Context, domain.DecisionSyncPeer) error {
	return errors.New("disk full")
}

func TestLinkRollsBackWhenTheStatusRowFails(t *testing.T) {
	peer := newFakePeer(t, "h_mini")
	path := decisionsync.PeerSecretsPath(t.TempDir())
	secrets, err := decisionsync.LoadPeerSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	peers := decisionsync.NewPeers(decisionsync.PeersDeps{
		Secrets: secrets, Store: failingUpsertStore{newMemPeerStore()}, API: decisionsync.NewHTTPClient(nil),
		HostID: "h_laptop", Now: fixedNow(t0),
	})
	if _, _, err := peers.Link(context.Background(), decisionsync.LinkInput{URL: peer.server.URL, Credential: "pw"}); !errors.Is(err, decisionsync.ErrLinkFailed) {
		t.Fatalf("err = %v, want ErrLinkFailed", err)
	}
	// After a restart, nothing links to the machine.
	reloaded, err := decisionsync.LoadPeerSecrets(path)
	if err != nil || len(reloaded.List()) != 0 {
		t.Fatalf("token file after a failed link = %+v, %v", reloaded.List(), err)
	}
	if len(peer.revoked) != 1 || peer.revoked[0] != "dev_minted" {
		t.Fatalf("minted token not revoked on the peer: %v", peer.revoked)
	}
}

func TestLinkWarnsAboutExistingSecretShapedEntries(t *testing.T) {
	peer := newFakePeer(t, "h_mini")
	f := newLinkFixture(t)
	ledger := ledgertest.NewMemLedger("h_laptop")
	raised := ledgertest.Raised("dec_leaky", "w-1", "laptop")
	var payload map[string]any
	_ = json.Unmarshal(raised.Payload, &payload)
	payload["context"] = "deploy with " + "gh" + "p_" + strings.Repeat("A1b2", 9)
	raised.Payload, _ = json.Marshal(payload)
	ledger.Append(raised, t0)
	ledger.Append(ledgertest.Raised("dec_clean", "w-1", "laptop"), t0)
	peers := decisionsync.NewPeers(decisionsync.PeersDeps{
		Secrets: f.secrets, Store: f.store, API: decisionsync.NewHTTPClient(nil), Entries: ledger,
		HostID: "h_laptop", Now: fixedNow(t0),
	})
	_, warnings, err := peers.Link(context.Background(), decisionsync.LinkInput{URL: peer.server.URL, Credential: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0].CallID != "dec_leaky" || warnings[0].Field != "context" {
		t.Fatalf("warnings = %+v, want one for dec_leaky's context", warnings)
	}
}

func TestApplyAcceptsSecretShapedRemoteEntry(t *testing.T) {
	ledger := ledgertest.NewMemLedger("h_laptop")
	entry := ledgertest.Received("h_mini", 1, "dec_1", domain.DecisionKindAnswered,
		`{"choice":"other","note":"`+"gh"+"p_"+strings.Repeat("A1b2", 9)+`"}`)
	res, err := ledger.ApplyDecisionEntries(context.Background(), "h_mini", []domain.DecisionEntry{entry}, t0)
	if err != nil || res.Applied != 1 {
		t.Fatalf("apply = %+v, %v; received entries are never checked for credentials", res, err)
	}
}

// fakeIssued is this machine's record of sync credentials issued to others.
type fakeIssued struct {
	held    map[string]int
	revoked []string
}

func (f *fakeIssued) RevokeDecisionSyncDevicesFor(_ context.Context, hostID string) (int, error) {
	n := f.held[hostID]
	delete(f.held, hostID)
	f.revoked = append(f.revoked, hostID)
	return n, nil
}

func newIssuedLinkFixture(t *testing.T, issued *fakeIssued) *linkFixture {
	t.Helper()
	f := newLinkFixture(t)
	f.peers = decisionsync.NewPeers(decisionsync.PeersDeps{
		Secrets: f.secrets, Store: f.store, API: decisionsync.NewHTTPClient(nil), Issued: issued,
		HostID: "h_laptop", MachineLabel: func() string { return "laptop" }, Now: fixedNow(t0),
		OnChange: func() { f.changes++ },
	})
	return f
}

func TestUnlinkRevokesCredentialsIssuedToThatMachine(t *testing.T) {
	ctx := context.Background()
	t.Run("OpposingLinks", func(t *testing.T) {
		peer := newFakePeer(t, "h_mini")
		issued := &fakeIssued{held: map[string]int{"h_mini": 1}}
		f := newIssuedLinkFixture(t, issued)
		if _, _, err := f.peers.Link(ctx, decisionsync.LinkInput{URL: peer.server.URL, Credential: "pw"}); err != nil {
			t.Fatal(err)
		}
		if err := f.peers.Unlink(ctx, "h_mini"); err != nil {
			t.Fatal(err)
		}
		if _, still := f.secrets.Get("h_mini"); still || len(issued.held) != 0 || len(peer.revoked) != 1 {
			t.Fatalf("token kept %v, issued left %v, revoked at peer %v", still, issued.held, peer.revoked)
		}
	})
	t.Run("InboundOnly", func(t *testing.T) {
		issued := &fakeIssued{held: map[string]int{"h_laptop2": 1}}
		f := newIssuedLinkFixture(t, issued)
		if err := f.store.UpsertDecisionSyncPeer(ctx, domain.DecisionSyncPeer{HostID: "h_laptop2", Direction: domain.DecisionSyncInbound}); err != nil {
			t.Fatal(err)
		}
		if err := f.peers.Unlink(ctx, "h_laptop2"); err != nil {
			t.Fatalf("inbound-only unlink: %v", err)
		}
		if f.store.get("h_laptop2").HostID != "" || len(issued.held) != 0 {
			t.Fatalf("row %+v, issued left %v", f.store.get("h_laptop2"), issued.held)
		}
	})
	t.Run("IssuedBeforeFirstExchange", func(t *testing.T) {
		issued := &fakeIssued{held: map[string]int{"h_new": 1}}
		f := newIssuedLinkFixture(t, issued)
		if err := f.peers.Unlink(ctx, "h_new"); err != nil {
			t.Fatalf("a credential with no row yet: %v", err)
		}
		if err := f.peers.Unlink(ctx, "h_new"); !errors.Is(err, decisionsync.ErrPeerNotLinked) {
			t.Fatalf("nothing left: err = %v, want ErrPeerNotLinked", err)
		}
	})
}
