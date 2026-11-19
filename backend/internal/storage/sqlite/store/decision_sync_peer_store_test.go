package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDecisionSyncPeerRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := syncBase

	if err := s.UpsertDecisionSyncPeer(ctx, domain.DecisionSyncPeer{
		HostID: "h_mini", Label: "mac-mini", URL: "http://10.0.0.2:3011", Direction: domain.DecisionSyncOutbound,
		DeviceID: "dev_1", CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	// An inbound exchange from a peer this machine links to never turns the
	// link into an inbound row.
	if err := s.RecordInboundDecisionSyncPeer(ctx, "h_mini", "renamed", 1, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordInboundDecisionSyncPeer(ctx, "h_laptop", "laptop", 1, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RecordDecisionSyncAttempt(ctx, "h_mini", domain.DecisionSyncAttempt{
		At: at.Add(2 * time.Minute), InSync: true, PeerLabel: "mac-mini", PeerVector: domain.DecisionVector{"h_mini": 4},
		ClockAheadMS: 420000, PeerProtocol: 1,
	}); err != nil || !ok {
		t.Fatalf("record success = %v, %v", ok, err)
	}
	peers, err := s.ListDecisionSyncPeers(ctx)
	if err != nil || len(peers) != 2 {
		t.Fatalf("peers = %+v, %v", peers, err)
	}
	laptop, mini := peers[0], peers[1]
	if laptop.HostID != "h_laptop" || laptop.Direction != domain.DecisionSyncInbound || laptop.Label != "laptop" || !laptop.LastSuccessAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("inbound peer = %+v", laptop)
	}
	if mini.Direction != domain.DecisionSyncOutbound || mini.Label != "mac-mini" || mini.URL != "http://10.0.0.2:3011" ||
		!mini.InSync || mini.PeerVector["h_mini"] != 4 || mini.ClockAheadMS != 420000 || mini.PeerProtocol != 1 ||
		!mini.LastSuccessAt.Equal(at.Add(2*time.Minute)) || mini.LastErrorCode != "" {
		t.Fatalf("outbound peer = %+v", mini)
	}

	if ok, err := s.RecordDecisionSyncAttempt(ctx, "h_mini", domain.DecisionSyncAttempt{At: at.Add(3 * time.Minute), ErrorCode: "unreachable"}); err != nil || !ok {
		t.Fatalf("record failure = %v, %v", ok, err)
	}
	mini, _, _ = s.GetDecisionSyncPeer(ctx, "h_mini")
	if mini.LastErrorCode != "unreachable" || mini.InSync || !mini.LastSuccessAt.Equal(at.Add(2*time.Minute)) || !mini.LastAttemptAt.Equal(at.Add(3*time.Minute)) {
		t.Fatalf("after failure = %+v", mini)
	}
	if ok, _ := s.RecordDecisionSyncAttempt(ctx, "h_gone", domain.DecisionSyncAttempt{At: at}); ok {
		t.Fatal("recorded an attempt for a peer with no row")
	}
	if ok, err := s.DeleteDecisionSyncPeer(ctx, "h_mini"); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if _, found, _ := s.GetDecisionSyncPeer(ctx, "h_mini"); found {
		t.Fatal("peer survived delete")
	}
}

func TestDecisionOriginLabelsUseNewestCall(t *testing.T) {
	s := newTestStore(t)
	older := decisionRaised("dent_1", "h_a", "dec_1", "w-1", "", "", syncBase)
	appendDecision(t, s, older, syncBase)
	newer := decisionRaised("dent_2", "h_a", "dec_2", "w-1", "", "", syncBase.Add(time.Hour))
	newer.Payload = []byte(`{"title":"t","context":"c","options":[],"machineLabel":"renamed","ownerSessionId":"w-1","ownerSessionName":"w-1"}`)
	appendDecision(t, s, newer, syncBase.Add(time.Hour))
	labels, err := s.DecisionOriginLabels(context.Background())
	if err != nil || labels["h_a"] != "renamed" {
		t.Fatalf("labels = %v, %v", labels, err)
	}
}
