package decisionsync

import (
	"context"
	"errors"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// unlinkRevokeTimeout bounds the best-effort revoke on the peer.
const unlinkRevokeTimeout = 5 * time.Second

// Link errors. Their messages are safe to show: they never carry a
// credential, a token or text from the peer.
var (
	ErrLinkSelf          = errors.New("that address is this machine")
	ErrLinkHostChanged   = errors.New("that address now belongs to a different machine than expected")
	ErrPeerNotLinked     = errors.New("no linked machine has that host id")
	ErrPeerInboundOnly   = errors.New("that machine links to this one; turn off its link on that machine, or revoke its \"Decision sync from\" device on this one")
	ErrLinkCredentialBad = errors.New("the machine refused the credential")
	ErrLinkUnreachable   = errors.New("could not reach the machine")
	ErrLinkIncompatible  = errors.New("the machine needs a newer Orchestrator to sync decisions")
	ErrLinkFailed        = errors.New("could not link the machine")
)

// PeerStore is the status table the link manager keeps in step with the
// token file.
type PeerStore interface {
	StatusSource
	UpsertDecisionSyncPeer(context.Context, domain.DecisionSyncPeer) error
	GetDecisionSyncPeer(context.Context, string) (domain.DecisionSyncPeer, bool, error)
	DeleteDecisionSyncPeer(context.Context, string) (bool, error)
}

// PeerAPI is what linking asks of a peer over the network.
type PeerAPI interface {
	Identity(ctx context.Context, baseURL string) (string, error)
	MintToken(ctx context.Context, baseURL, expectedHostID, credential string, in MintRequest) (MintResult, error)
	RevokeOwnToken(ctx context.Context, peer StoredPeer) error
}

// IssuedCredentials is this machine's own record of the sync credentials it
// issued to other machines.
type IssuedCredentials interface {
	// RevokeDecisionSyncDevicesFor removes every decision sync credential
	// issued to hostID and returns how many it removed.
	RevokeDecisionSyncDevicesFor(ctx context.Context, hostID string) (int, error)
}

// PeersDeps configures the link manager.
type PeersDeps struct {
	Secrets *PeerSecrets
	Store   PeerStore
	API     PeerAPI
	// Issued, when set, lets Unlink revoke the credentials this machine
	// issued to the other one, which stops a link that machine made here.
	Issued       IssuedCredentials
	HostID       string
	MachineLabel func() string
	Now          func() time.Time
	// OnChange runs after a link is added or removed.
	OnChange func()
}

// Peers links and unlinks machines. It holds each link's scoped token in the
// owner-only token file and its status in the store.
type Peers struct {
	deps PeersDeps
}

// NewPeers returns a link manager over deps.
func NewPeers(deps PeersDeps) *Peers {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.MachineLabel == nil {
		deps.MachineLabel = func() string { return deps.HostID }
	}
	return &Peers{deps: deps}
}

// LinkInput is what the person gives to link a machine. Credential is a full
// credential for that machine (its password, an account token or a device
// token). It is presented once to mint a scoped token and then dropped.
type LinkInput struct {
	URL            string
	ExpectedHostID string
	Label          string
	Credential     string
}

// SecretWarning names an existing entry that looks like it holds a
// credential. It is filled only once the credential guard exists (plan Q4).
type SecretWarning struct {
	CallID string
	Field  string
}

// Link probes the machine's identity, refuses this machine itself or a host id
// other than expected, asks the machine to mint a decision-sync token bound
// to this machine's host id, and stores only that token.
func (m *Peers) Link(ctx context.Context, in LinkInput) (PeerStatus, []SecretWarning, error) {
	base, err := NormalizePeerURL(in.URL)
	if err != nil {
		return PeerStatus{}, nil, err
	}
	hostID, err := m.deps.API.Identity(ctx, base)
	if err != nil {
		return PeerStatus{}, nil, linkError(err)
	}
	if hostID == m.deps.HostID {
		return PeerStatus{}, nil, ErrLinkSelf
	}
	if expected := strings.TrimSpace(in.ExpectedHostID); expected != "" && expected != hostID {
		return PeerStatus{}, nil, ErrLinkHostChanged
	}
	minted, err := m.deps.API.MintToken(ctx, base, hostID, in.Credential, MintRequest{
		Name: "Decision sync from " + m.deps.MachineLabel(), Platform: runtime.GOOS, ClientID: m.deps.HostID,
	})
	if err != nil {
		return PeerStatus{}, nil, linkError(err)
	}
	// An empty label is filled with the machine's own name on its first sync.
	label := strings.TrimSpace(in.Label)
	if existing, ok := m.deps.Secrets.Get(hostID); ok && label == "" {
		label = existing.Label
	}
	stored := StoredPeer{HostID: hostID, URL: base, Label: label, DeviceID: minted.DeviceID, Token: minted.Token}
	previous, hadPrevious := m.deps.Secrets.Get(hostID)
	// From here a failure undoes everything: the token file, the status row
	// and the token minted on the peer, so a failed link never syncs later.
	rollback := func() {
		undo := context.WithoutCancel(ctx)
		if hadPrevious {
			_ = m.deps.Secrets.Put(previous)
		} else {
			_, _, _ = m.deps.Secrets.Delete(hostID)
		}
		revokeCtx, cancel := context.WithTimeout(undo, unlinkRevokeTimeout)
		defer cancel()
		_ = m.deps.API.RevokeOwnToken(revokeCtx, stored)
	}
	if err := m.deps.Secrets.Put(stored); err != nil {
		rollback()
		return PeerStatus{}, nil, ErrLinkFailed
	}
	peer := domain.DecisionSyncPeer{
		HostID: hostID, Label: label, URL: base, Direction: domain.DecisionSyncOutbound,
		DeviceID: minted.DeviceID, CreatedAt: m.deps.Now(),
	}
	if err := m.deps.Store.UpsertDecisionSyncPeer(context.WithoutCancel(ctx), peer); err != nil {
		rollback()
		return PeerStatus{}, nil, ErrLinkFailed
	}
	if m.deps.OnChange != nil {
		m.deps.OnChange()
	}
	if peer.Label == "" {
		peer.Label = defaultPeerLabel(base)
	}
	return PeerStatus{DecisionSyncPeer: peer, State: PeerLinkedBehind}, nil, nil
}

// Unlink stops syncing with a machine in both directions: it revokes every
// sync credential this machine issued to it (a link that machine made here,
// even one not used yet), removes the link's status row and token here, then
// asks the machine to revoke that token, giving up after a few seconds.
// Every entry stays on both machines.
func (m *Peers) Unlink(ctx context.Context, hostID string) error {
	stored, hadToken := m.deps.Secrets.Get(hostID)
	issued := 0
	if m.deps.Issued != nil {
		n, err := m.deps.Issued.RevokeDecisionSyncDevicesFor(ctx, hostID)
		if err != nil {
			return ErrLinkFailed
		}
		issued = n
	}
	if !hadToken && m.deps.Issued == nil {
		// A peer that only links to this machine holds a token this machine
		// issued; deleting its row here would leave that access in place.
		row, found, err := m.deps.Store.GetDecisionSyncPeer(ctx, hostID)
		if err != nil {
			return ErrLinkFailed
		}
		if found && row.Direction == domain.DecisionSyncInbound {
			return ErrPeerInboundOnly
		}
	}
	hadRow, err := m.deps.Store.DeleteDecisionSyncPeer(ctx, hostID)
	if err != nil {
		return ErrLinkFailed
	}
	if hadToken {
		if _, _, err := m.deps.Secrets.Delete(hostID); err != nil {
			return ErrLinkFailed
		}
	}
	if !hadToken && !hadRow && issued == 0 {
		return ErrPeerNotLinked
	}
	if m.deps.OnChange != nil {
		m.deps.OnChange()
	}
	if hadToken {
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlinkRevokeTimeout)
		defer cancel()
		_ = m.deps.API.RevokeOwnToken(revokeCtx, stored)
	}
	return nil
}

// List returns every peer with its derived state.
func (m *Peers) List(ctx context.Context) ([]PeerStatus, error) {
	status, err := ReadStatus(ctx, m.deps.Store, m.deps.HostID, m.deps.MachineLabel())
	if err != nil {
		return nil, err
	}
	return status.Peers, nil
}

// Linked returns every outbound link with its token, for the scheduler.
func (m *Peers) Linked() []StoredPeer {
	return m.deps.Secrets.List()
}

// Get returns one outbound link with its token.
func (m *Peers) Get(hostID string) (StoredPeer, bool) {
	return m.deps.Secrets.Get(hostID)
}

func defaultPeerLabel(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return base
	}
	return u.Hostname()
}

// linkError turns a peer failure into a fixed, credential-free error.
func linkError(err error) error {
	switch ErrorCode(err) {
	case ErrorUnauthorized, ErrorRevoked:
		return ErrLinkCredentialBad
	case ErrorUnreachable:
		return ErrLinkUnreachable
	case ErrorIncompatible:
		return ErrLinkIncompatible
	case ErrorIdentityChanged:
		return ErrLinkHostChanged
	default:
		return ErrLinkFailed
	}
}

// PeerService is the peer routes' surface: the link manager plus on-demand
// sync from the scheduler.
type PeerService struct {
	*Peers
	*Scheduler
}
