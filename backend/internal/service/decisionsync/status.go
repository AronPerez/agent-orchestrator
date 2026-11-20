package decisionsync

import (
	"context"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PeerState is a peer's freshness in words the UI and the CLI share.
type PeerState string

// Peer states, derived from the status row at read time.
const (
	PeerLinkedInSync     PeerState = "linked_in_sync"
	PeerLinkedBehind     PeerState = "linked_behind"
	PeerUnreachable      PeerState = "unreachable"
	PeerRevoked          PeerState = "revoked"
	PeerIncompatible     PeerState = "incompatible"
	PeerIdentityConflict PeerState = "identity_conflict"
	PeerInbound          PeerState = "inbound"
)

// Error codes recorded in a peer's last_error_code.
const (
	ErrorUnreachable     = "unreachable"
	ErrorRevoked         = "revoked"
	ErrorUnauthorized    = "unauthorized"
	ErrorIncompatible    = "incompatible"
	ErrorIdentityChanged = "identity_changed"
	ErrorServer          = "server_error"
)

// StatusSource is what ReadStatus reads. The SQLite store implements it.
type StatusSource interface {
	ListDecisionSyncPeers(context.Context) ([]domain.DecisionSyncPeer, error)
	DecisionOriginsLastHeard(context.Context) (map[string]time.Time, error)
	DecisionOriginLabels(context.Context) (map[string]string, error)
	ListDecisionSyncConflicts(context.Context) ([]domain.DecisionSyncConflict, error)
}

// PeerStatus is one peer and its derived state.
type PeerStatus struct {
	domain.DecisionSyncPeer
	State PeerState
}

// OriginStatus is one machine whose decisions this ledger holds, and when the
// newest of them was written.
type OriginStatus struct {
	HostID       string
	MachineLabel string
	LastHeardAt  time.Time
}

// Status is this machine's sync freshness. It holds no secrets.
type Status struct {
	HostID       string
	MachineLabel string
	Peers        []PeerStatus
	Origins      []OriginStatus
	Conflicts    []domain.DecisionSyncConflict
}

// ReadStatus assembles the freshness view for this machine.
func ReadStatus(ctx context.Context, src StatusSource, hostID, machineLabel string) (Status, error) {
	peers, err := src.ListDecisionSyncPeers(ctx)
	if err != nil {
		return Status{}, err
	}
	conflicts, err := src.ListDecisionSyncConflicts(ctx)
	if err != nil {
		return Status{}, err
	}
	heard, err := src.DecisionOriginsLastHeard(ctx)
	if err != nil {
		return Status{}, err
	}
	labels, err := src.DecisionOriginLabels(ctx)
	if err != nil {
		return Status{}, err
	}
	if labels == nil {
		labels = map[string]string{}
	}
	out := Status{HostID: hostID, MachineLabel: machineLabel, Conflicts: conflicts}
	for _, peer := range peers {
		out.Peers = append(out.Peers, PeerStatus{DecisionSyncPeer: peer, State: PeerStateOf(peer, conflicts)})
		if peer.Label != "" {
			labels[peer.HostID] = peer.Label
		}
	}
	labels[hostID] = machineLabel
	for origin, at := range heard {
		out.Origins = append(out.Origins, OriginStatus{HostID: origin, MachineLabel: labels[origin], LastHeardAt: at})
	}
	slices.SortFunc(out.Origins, func(a, b OriginStatus) int {
		switch {
		case a.HostID < b.HostID:
			return -1
		case a.HostID > b.HostID:
			return 1
		}
		return 0
	})
	return out, nil
}

// PeerStateOf derives a peer's state. A conflict involving the peer outranks
// everything; then an inbound-only peer; then the last error; then whether
// the last exchange ended in sync.
func PeerStateOf(peer domain.DecisionSyncPeer, conflicts []domain.DecisionSyncConflict) PeerState {
	for _, c := range conflicts {
		if c.PeerHostID == peer.HostID || c.Origin == peer.HostID {
			return PeerIdentityConflict
		}
	}
	if peer.Direction == domain.DecisionSyncInbound {
		return PeerInbound
	}
	switch peer.LastErrorCode {
	case "":
	case ErrorRevoked, ErrorUnauthorized:
		return PeerRevoked
	case ErrorIncompatible:
		return PeerIncompatible
	default:
		return PeerUnreachable
	}
	if peer.InSync && !peer.LastSuccessAt.IsZero() {
		return PeerLinkedInSync
	}
	return PeerLinkedBehind
}
