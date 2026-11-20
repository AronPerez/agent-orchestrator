package decisionsync_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

// fakeTimer hands the server a timer channel the test fires.
type fakeTimer struct {
	mu    sync.Mutex
	asked []time.Duration
	fire  chan time.Time
}

func newFakeTimer() *fakeTimer { return &fakeTimer{fire: make(chan time.Time, 1)} }

func (f *fakeTimer) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.asked = append(f.asked, d)
	f.mu.Unlock()
	return f.fire
}

type recorded struct {
	host, label string
	protocol    int
}

type fakeRecorder struct {
	mu   sync.Mutex
	rows []recorded
}

func (f *fakeRecorder) RecordInboundDecisionSyncPeer(_ context.Context, host, label string, protocol int, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, recorded{host, label, protocol})
	return nil
}

func waitingServer(ledger *ledgertest.MemLedger, timer *fakeTimer, now func() time.Time) *decisionsync.Server {
	return decisionsync.NewServer(decisionsync.ServerDeps{Ledger: ledger, HostID: ledger.HostID, Now: now, After: timer.After})
}

type exchangeResult struct {
	resp decisionsync.ExchangeResponse
	err  error
}

func exchangeAsync(ctx context.Context, s *decisionsync.Server, wait int) <-chan exchangeResult {
	out := make(chan exchangeResult, 1)
	go func() {
		resp, err := s.Exchange(ctx, "h_a", decisionsync.ExchangeRequest{Protocol: 1, HostID: "h_a", WaitSeconds: wait})
		out <- exchangeResult{resp, err}
	}()
	return out
}

func TestDecisionSyncExchangeWaitsForChange(t *testing.T) {
	ledger := ledgertest.NewMemLedger("h_b")
	timer := newFakeTimer()
	server := waitingServer(ledger, timer, fixedNow(t0))

	done := exchangeAsync(context.Background(), server, 25)
	select {
	case r := <-done:
		t.Fatalf("exchange with nothing to send returned at once: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	written := ledger.Append(ledgertest.Future("dec_1"), t0.Add(2*time.Second))
	server.Changed()
	select {
	case r := <-done:
		if r.err != nil || len(r.resp.Entries) != 1 || r.resp.Entries[0].ID != written.ID {
			t.Fatalf("woken exchange = %+v, %v", r.resp, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exchange did not wake on Changed")
	}

	// Nothing new: the wait ends empty when its timer fires.
	timer2 := newFakeTimer()
	server2 := waitingServer(ledger, timer2, fixedNow(t0))
	done = func() <-chan exchangeResult {
		out := make(chan exchangeResult, 1)
		go func() {
			resp, err := server2.Exchange(context.Background(), "h_a", decisionsync.ExchangeRequest{
				Protocol: 1, HostID: "h_a", Have: domain.DecisionVector{"h_b": 1}, WaitSeconds: 25,
			})
			out <- exchangeResult{resp, err}
		}()
		return out
	}()
	// A change the waiter already holds re-reads and keeps waiting.
	time.Sleep(20 * time.Millisecond)
	server2.Changed()
	select {
	case r := <-done:
		t.Fatalf("exchange returned on an irrelevant change: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	timer2.fire <- t0.Add(25 * time.Second)
	select {
	case r := <-done:
		if r.err != nil || len(r.resp.Entries) != 0 {
			t.Fatalf("timed-out exchange = %+v, %v", r.resp, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exchange did not end when its wait ran out")
	}
	if len(timer2.asked) != 1 || timer2.asked[0] != 25*time.Second {
		t.Fatalf("wait asked = %v, want one 25s timer", timer2.asked)
	}
}

func TestDecisionSyncExchangeClampsWait(t *testing.T) {
	ledger := ledgertest.NewMemLedger("h_b")
	timer := newFakeTimer()
	server := waitingServer(ledger, timer, fixedNow(t0))
	done := exchangeAsync(context.Background(), server, 600)
	time.Sleep(20 * time.Millisecond)
	timer.fire <- t0
	<-done
	if len(timer.asked) != 1 || timer.asked[0] != decisionsync.MaxWaitSeconds*time.Second {
		t.Fatalf("wait asked = %v, want the 25s cap", timer.asked)
	}
}

func TestDecisionSyncExchangeWaitEndsOnShutdown(t *testing.T) {
	ledger := ledgertest.NewMemLedger("h_b")
	server := waitingServer(ledger, newFakeTimer(), fixedNow(t0))
	done := exchangeAsync(context.Background(), server, 25)
	time.Sleep(20 * time.Millisecond)
	server.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("stopped exchange err = %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exchange outlived Stop")
	}
	// Request cancellation ends a wait too.
	server2 := waitingServer(ledger, newFakeTimer(), fixedNow(t0))
	ctx, cancel := context.WithCancel(context.Background())
	done = exchangeAsync(ctx, server2, 25)
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("cancelled exchange returned no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exchange outlived its request")
	}
}

func TestDecisionSyncRecordsInboundPeer(t *testing.T) {
	ledger := ledgertest.NewMemLedger("h_b")
	rec := &fakeRecorder{}
	now := t0
	server := decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: ledger, HostID: "h_b", Peers: rec, Now: func() time.Time { return now },
	})
	exchange := func(label string) {
		t.Helper()
		if _, err := server.Exchange(context.Background(), "h_a", decisionsync.ExchangeRequest{Protocol: 1, HostID: "h_a", MachineLabel: label}); err != nil {
			t.Fatal(err)
		}
	}
	exchange("laptop")
	exchange("laptop")
	now = now.Add(30 * time.Second)
	exchange("laptop")
	if len(rec.rows) != 1 || rec.rows[0] != (recorded{"h_a", "laptop", 1}) {
		t.Fatalf("rows within a minute = %+v, want one", rec.rows)
	}
	exchange("renamed")
	now = now.Add(2 * time.Minute)
	exchange("renamed")
	if len(rec.rows) != 3 || rec.rows[1].label != "renamed" {
		t.Fatalf("rows = %+v, want a rename and a refresh recorded", rec.rows)
	}
}

func TestPeerStateOf(t *testing.T) {
	ok := domain.DecisionSyncPeer{HostID: "h_p", Direction: domain.DecisionSyncOutbound, InSync: true, LastSuccessAt: t0}
	cases := []struct {
		name      string
		peer      domain.DecisionSyncPeer
		conflicts []domain.DecisionSyncConflict
		want      decisionsync.PeerState
	}{
		{"in sync", ok, nil, decisionsync.PeerLinkedInSync},
		{"behind", domain.DecisionSyncPeer{HostID: "h_p", Direction: domain.DecisionSyncOutbound, LastSuccessAt: t0}, nil, decisionsync.PeerLinkedBehind},
		{"never synced", domain.DecisionSyncPeer{HostID: "h_p", Direction: domain.DecisionSyncOutbound}, nil, decisionsync.PeerLinkedBehind},
		{"revoked", withError(ok, decisionsync.ErrorRevoked), nil, decisionsync.PeerRevoked},
		{"unauthorized", withError(ok, decisionsync.ErrorUnauthorized), nil, decisionsync.PeerRevoked},
		{"incompatible", withError(ok, decisionsync.ErrorIncompatible), nil, decisionsync.PeerIncompatible},
		{"unreachable", withError(ok, decisionsync.ErrorUnreachable), nil, decisionsync.PeerUnreachable},
		{"server error", withError(ok, decisionsync.ErrorServer), nil, decisionsync.PeerUnreachable},
		{"inbound", domain.DecisionSyncPeer{HostID: "h_p", Direction: domain.DecisionSyncInbound}, nil, decisionsync.PeerInbound},
		{"conflict via peer", ok, []domain.DecisionSyncConflict{{Origin: "h_x", PeerHostID: "h_p"}}, decisionsync.PeerIdentityConflict},
		{"conflict on its origin", ok, []domain.DecisionSyncConflict{{Origin: "h_p", PeerHostID: "h_y"}}, decisionsync.PeerIdentityConflict},
		{"unrelated conflict", ok, []domain.DecisionSyncConflict{{Origin: "h_x", PeerHostID: "h_y"}}, decisionsync.PeerLinkedInSync},
	}
	for _, tc := range cases {
		if got := decisionsync.PeerStateOf(tc.peer, tc.conflicts); got != tc.want {
			t.Errorf("%s: state = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func withError(p domain.DecisionSyncPeer, code string) domain.DecisionSyncPeer {
	p.LastErrorCode = code
	return p
}
