package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertDecisionSyncPeer creates or updates a peer's identity columns. Status
// columns (attempts, freshness) are left as they are on update.
func (s *Store) UpsertDecisionSyncPeer(ctx context.Context, peer domain.DecisionSyncPeer) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertDecisionSyncPeer(ctx, gen.UpsertDecisionSyncPeerParams{
		HostID: peer.HostID, Label: peer.Label, URL: peer.URL, Direction: string(peer.Direction),
		DeviceID: peer.DeviceID, CreatedAt: peer.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("upsert decision sync peer %s: %w", peer.HostID, err)
	}
	return nil
}

// RecordInboundDecisionSyncPeer notes that hostID exchanged with this machine.
// It creates an inbound row, or refreshes one, but never changes a peer this
// machine links to itself.
func (s *Store) RecordInboundDecisionSyncPeer(ctx context.Context, hostID, label string, protocol int, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.RecordInboundDecisionSyncPeer(ctx, gen.RecordInboundDecisionSyncPeerParams{
		HostID: hostID, Label: label, At: at.UTC(), PeerProtocol: int64(protocol),
	}); err != nil {
		return fmt.Errorf("record inbound decision sync peer %s: %w", hostID, err)
	}
	return nil
}

// RecordDecisionSyncAttempt stores the result of one exchange with hostID. It
// reports false when the peer has no row (it was unlinked meanwhile).
func (s *Store) RecordDecisionSyncAttempt(ctx context.Context, hostID string, attempt domain.DecisionSyncAttempt) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	at := sql.NullTime{Time: attempt.At.UTC(), Valid: true}
	var n int64
	var err error
	if attempt.ErrorCode != "" {
		n, err = s.qw.RecordDecisionSyncFailure(ctx, gen.RecordDecisionSyncFailureParams{
			At: at, ErrorCode: attempt.ErrorCode, HostID: hostID,
		})
	} else {
		vector := attempt.PeerVector
		if vector == nil {
			vector = domain.DecisionVector{}
		}
		raw, encErr := json.Marshal(vector)
		if encErr != nil {
			return false, fmt.Errorf("encode peer vector: %w", encErr)
		}
		n, err = s.qw.RecordDecisionSyncSuccess(ctx, gen.RecordDecisionSyncSuccessParams{
			At: at, InSync: boolInt(attempt.InSync), PeerVector: string(raw),
			ClockAheadMs: attempt.ClockAheadMS, PeerProtocol: int64(attempt.PeerProtocol),
			Label: attempt.PeerLabel, HostID: hostID,
		})
	}
	if err != nil {
		return false, fmt.Errorf("record decision sync attempt for %s: %w", hostID, err)
	}
	return n == 1, nil
}

// ListDecisionSyncPeers returns every peer's status, by host id.
func (s *Store) ListDecisionSyncPeers(ctx context.Context) ([]domain.DecisionSyncPeer, error) {
	rows, err := s.qr.ListDecisionSyncPeers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list decision sync peers: %w", err)
	}
	out := make([]domain.DecisionSyncPeer, len(rows))
	for i, row := range rows {
		out[i] = decisionSyncPeerFromGen(row)
	}
	return out, nil
}

// GetDecisionSyncPeer returns one peer's status when it exists.
func (s *Store) GetDecisionSyncPeer(ctx context.Context, hostID string) (domain.DecisionSyncPeer, bool, error) {
	row, err := s.qr.GetDecisionSyncPeer(ctx, hostID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DecisionSyncPeer{}, false, nil
	}
	if err != nil {
		return domain.DecisionSyncPeer{}, false, fmt.Errorf("get decision sync peer %s: %w", hostID, err)
	}
	return decisionSyncPeerFromGen(row), true, nil
}

// DeleteDecisionSyncPeer removes a peer's status row. Entries are kept.
func (s *Store) DeleteDecisionSyncPeer(ctx context.Context, hostID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteDecisionSyncPeer(ctx, hostID)
	if err != nil {
		return false, fmt.Errorf("delete decision sync peer %s: %w", hostID, err)
	}
	return n == 1, nil
}

// DecisionOriginLabels returns, per origin, the machine label on its newest
// call, for naming machines this one only hears of through relays.
func (s *Store) DecisionOriginLabels(ctx context.Context) (map[string]string, error) {
	rows, err := s.qr.DecisionOriginLabels(ctx)
	if err != nil {
		return nil, fmt.Errorf("decision origin labels: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Origin] = row.MachineLabel
	}
	return out, nil
}

func decisionSyncPeerFromGen(row gen.DecisionSyncPeer) domain.DecisionSyncPeer {
	vector := domain.DecisionVector{}
	_ = json.Unmarshal([]byte(row.PeerVector), &vector)
	return domain.DecisionSyncPeer{
		HostID: row.HostID, Label: row.Label, URL: row.URL,
		Direction: domain.DecisionSyncDirection(row.Direction), DeviceID: row.DeviceID,
		CreatedAt: row.CreatedAt, LastAttemptAt: row.LastAttemptAt.Time, LastSuccessAt: row.LastSuccessAt.Time,
		LastErrorCode: row.LastErrorCode, InSync: row.InSync != 0, PeerVector: vector,
		ClockAheadMS: row.ClockAheadMs, PeerProtocol: int(row.PeerProtocol),
	}
}
