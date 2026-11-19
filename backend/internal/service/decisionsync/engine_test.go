package decisionsync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

var t0 = time.UnixMilli(1_700_000_000_000).UTC()

func fixedNow(at time.Time) func() time.Time { return func() time.Time { return at } }

// machine is one ledger with its engine and server, as a daemon wires them.
type machine struct {
	ledger  *ledgertest.MemLedger
	engine  *decisionsync.Engine
	server  *decisionsync.Server
	applied int
}

func newMachine(hostID string, now func() time.Time) *machine {
	m := &machine{ledger: ledgertest.NewMemLedger(hostID)}
	onApplied := func(string, domain.DecisionApplyResult) { m.applied++ }
	m.engine = decisionsync.NewEngine(decisionsync.EngineDeps{
		Ledger: m.ledger, HostID: hostID, MachineLabel: func() string { return hostID + "-label" }, Now: now, OnApplied: onApplied,
	})
	m.server = decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: m.ledger, HostID: hostID, MachineLabel: func() string { return hostID + "-label" }, Now: now, OnApplied: onApplied,
	})
	return m
}

func (m *machine) remoteTo(peer *machine) *ledgertest.DirectRemote {
	return &ledgertest.DirectRemote{Server: peer.server, CallerID: m.ledger.HostID}
}

func (m *machine) syncWith(t *testing.T, peer *machine) decisionsync.Outcome {
	t.Helper()
	out, err := m.engine.Sync(context.Background(), peer.ledger.HostID, m.remoteTo(peer))
	if err != nil {
		t.Fatalf("%s sync with %s: %v", m.ledger.HostID, peer.ledger.HostID, err)
	}
	return out
}

func appendN(m *machine, n int, callID string) {
	for i := 0; i < n; i++ {
		m.ledger.Append(ledgertest.Future(callID), t0)
	}
}

func TestEngineSyncPullsThenPushes(t *testing.T) {
	a, b := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0))
	appendN(a, 3, "dec_a")
	appendN(b, 2, "dec_b")
	out := a.syncWith(t, b)
	if out.Pulled != 2 || out.Pushed != 3 || !out.InSync {
		t.Fatalf("outcome = %+v, want pulled 2, pushed 3, in sync", out)
	}
	if len(a.ledger.Entries()) != 5 || len(b.ledger.Entries()) != 5 {
		t.Fatalf("entries a=%d b=%d, want 5 each", len(a.ledger.Entries()), len(b.ledger.Entries()))
	}
	if out.PeerLabel != "h_b-label" || out.PeerVector["h_a"] != 3 || out.PeerVector["h_b"] != 2 {
		t.Fatalf("peer = %q %v", out.PeerLabel, out.PeerVector)
	}
}

func TestEngineSyncRelaysThirdOrigins(t *testing.T) {
	a, b, c := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0)), newMachine("h_c", fixedNow(t0))
	appendN(c, 2, "dec_c")
	c.syncWith(t, b)
	out := a.syncWith(t, b)
	if out.Pulled != 2 {
		t.Fatalf("pulled = %d, want c's 2 entries relayed by b", out.Pulled)
	}
	v, _ := a.ledger.DecisionVector(context.Background())
	if v["h_c"] != 2 {
		t.Fatalf("a vector = %v, want h_c:2", v)
	}
}

func TestEngineSyncPagesLargeBacklog(t *testing.T) {
	a, b := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0))
	appendN(b, 1234, "dec_b")
	remote := a.remoteTo(b)
	out, err := a.engine.Sync(context.Background(), "h_b", remote)
	if err != nil {
		t.Fatal(err)
	}
	if out.Pulled != 1234 || remote.Exchanges != 3 || remote.Pushes != 0 {
		t.Fatalf("pulled %d in %d exchanges and %d pushes, want 1234 in 3 and 0", out.Pulled, remote.Exchanges, remote.Pushes)
	}
	// Pushing the backlog back to an empty peer pages the same way.
	c := newMachine("h_c", fixedNow(t0))
	toC := a.remoteTo(c)
	if _, err := a.engine.Sync(context.Background(), "h_c", toC); err != nil {
		t.Fatal(err)
	}
	if toC.Pushes != 3 || len(c.ledger.Entries()) != 1234 {
		t.Fatalf("pushes = %d, c entries = %d", toC.Pushes, len(c.ledger.Entries()))
	}
}

func TestEngineSyncRefusesChangedIdentity(t *testing.T) {
	a, b := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0))
	appendN(b, 1, "dec_b")
	_, err := a.engine.Sync(context.Background(), "h_expected", a.remoteTo(b))
	if !errors.Is(err, decisionsync.ErrPeerIdentityChanged) {
		t.Fatalf("err = %v, want ErrPeerIdentityChanged", err)
	}
	if len(a.ledger.Entries()) != 0 {
		t.Fatal("entries applied from a peer with the wrong identity")
	}
}

func TestEngineSyncStopsCleanlyOnCancel(t *testing.T) {
	a, b := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0))
	appendN(b, 1200, "dec_b")
	ctx, cancel := context.WithCancel(context.Background())
	remote := a.remoteTo(b)
	remote.BeforeCall = func(n int) error {
		if n == 2 {
			cancel()
		}
		return nil
	}
	_, err := a.engine.Sync(ctx, "h_b", remote)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := len(a.ledger.Entries()); got != 500 && got != 1000 {
		t.Fatalf("entries after cancel = %d, want whole pages kept", got)
	}
	a.syncWith(t, b)
	if got := len(a.ledger.Entries()); got != 1200 {
		t.Fatalf("entries after resume = %d, want 1200", got)
	}
}

func TestServerExchangeReturnsOnlyMissing(t *testing.T) {
	b := newMachine("h_b", fixedNow(t0))
	appendN(b, 5, "dec_b")
	resp, err := b.server.Exchange(context.Background(), "h_a", decisionsync.ExchangeRequest{
		Protocol: 1, HostID: "h_a", Have: domain.DecisionVector{"h_b": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 2 || resp.Entries[0].OriginSeq != 4 || resp.More || resp.Have["h_b"] != 5 || resp.HostID != "h_b" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestServerRefusesSameIdentityAndMismatch(t *testing.T) {
	b := newMachine("h_b", fixedNow(t0))
	if _, err := b.server.Exchange(context.Background(), "h_b", decisionsync.ExchangeRequest{HostID: "h_b"}); !errors.Is(err, decisionsync.ErrSameIdentity) {
		t.Fatalf("same identity err = %v", err)
	}
	if _, err := b.server.Exchange(context.Background(), "h_a", decisionsync.ExchangeRequest{HostID: "h_x"}); !errors.Is(err, decisionsync.ErrPeerIdentityMismatch) {
		t.Fatalf("mismatch err = %v", err)
	}
	if _, err := b.server.Push(context.Background(), "h_a", decisionsync.PushRequest{HostID: "h_x"}); !errors.Is(err, decisionsync.ErrPeerIdentityMismatch) {
		t.Fatalf("push mismatch err = %v", err)
	}
}

func TestServerPushRefusesOversizedPage(t *testing.T) {
	b := newMachine("h_b", fixedNow(t0))
	entries := make([]domain.DecisionEntry, domain.MaxDecisionSyncPageEntries+1)
	if _, err := b.server.Push(context.Background(), "h_a", decisionsync.PushRequest{HostID: "h_a", Entries: entries}); !errors.Is(err, decisionsync.ErrPageTooLarge) {
		t.Fatalf("err = %v, want ErrPageTooLarge", err)
	}
}

func TestEngineReportsClockAhead(t *testing.T) {
	for _, tc := range []struct {
		ahead time.Duration
		want  time.Duration
	}{{7 * time.Minute, 7 * time.Minute}, {3 * time.Minute, 0}} {
		a := newMachine("h_a", fixedNow(t0))
		b := newMachine("h_b", fixedNow(t0.Add(tc.ahead)))
		b.ledger.Append(ledgertest.Future("dec_b"), t0.Add(tc.ahead))
		out := a.syncWith(t, b)
		if out.ClockAhead != tc.want {
			t.Fatalf("ahead %v: ClockAhead = %v, want %v", tc.ahead, out.ClockAhead, tc.want)
		}
	}
}

func TestOnAppliedCalledOnlyWhenSomethingApplied(t *testing.T) {
	a, b := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0))
	a.syncWith(t, b)
	if a.applied != 0 || b.applied != 0 {
		t.Fatalf("applied hooks on empty sync: a=%d b=%d", a.applied, b.applied)
	}
	appendN(b, 1, "dec_b")
	appendN(a, 1, "dec_a")
	a.syncWith(t, b)
	if a.applied != 1 || b.applied != 1 {
		t.Fatalf("applied hooks: a=%d b=%d, want 1 each", a.applied, b.applied)
	}
	a.syncWith(t, b)
	if a.applied != 1 || b.applied != 1 {
		t.Fatalf("applied hooks after a no-op sync: a=%d b=%d", a.applied, b.applied)
	}
}

func TestEngineSyncDetectsForkAtEqualSequence(t *testing.T) {
	a, b := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0))
	// b holds a's identity's first entry under another id: a copied data
	// directory that both machines then wrote to.
	a.ledger.Append(ledgertest.Future("dec_1"), t0)
	twin := ledgertest.NewMemLedger("h_a")
	forked := twin.Append(ledgertest.Future("dec_1"), t0)
	if _, err := b.ledger.ApplyDecisionEntries(context.Background(), "h_twin", []domain.DecisionEntry{forked}, t0); err != nil {
		t.Fatal(err)
	}
	out := a.syncWith(t, b)
	if len(out.Conflicts) != 1 || len(b.ledger.Conflicts()) != 1 || out.InSync {
		t.Fatalf("outcome conflicts = %+v, b conflicts = %+v; want one each and not in sync", out.Conflicts, b.ledger.Conflicts())
	}
}

// hookLedger runs before once, just before the first page read: an append the
// server makes while an exchange is in flight.
type hookLedger struct {
	*ledgertest.MemLedger
	before func()
}

func (h *hookLedger) ListDecisionEntriesAfter(ctx context.Context, have domain.DecisionVector, n, b int) ([]domain.DecisionEntry, bool, error) {
	if h.before != nil {
		hook := h.before
		h.before = nil
		hook()
	}
	return h.MemLedger.ListDecisionEntriesAfter(ctx, have, n, b)
}

func TestExchangeDetectsForkAppendedDuringExchange(t *testing.T) {
	// x is the origin and serves; c holds x:1..2 and a forked x:3 it got from
	// a copy of x's data directory.
	x := ledgertest.NewMemLedger("h_x")
	x.Append(ledgertest.Future("dec_1"), t0)
	x.Append(ledgertest.Future("dec_1"), t0.Add(time.Second))
	twin := ledgertest.NewMemLedger("h_x")
	if _, err := twin.ApplyDecisionEntries(context.Background(), "h_copy", x.Entries(), t0); err != nil {
		t.Fatal(err)
	}
	twin.Append(ledgertest.Future("dec_1"), t0.Add(2*time.Second))
	c := newMachine("h_c", fixedNow(t0))
	if _, err := c.ledger.ApplyDecisionEntries(context.Background(), "h_twin", twin.Entries(), t0); err != nil {
		t.Fatal(err)
	}

	var ownX4 domain.DecisionEntry
	server := decisionsync.NewServer(decisionsync.ServerDeps{
		HostID: "h_x", Now: fixedNow(t0),
		Ledger: &hookLedger{MemLedger: x, before: func() {
			x.Append(ledgertest.Future("dec_1"), t0.Add(3*time.Second))
			ownX4 = x.Append(ledgertest.Future("dec_1"), t0.Add(4*time.Second))
		}},
	})
	if _, err := c.engine.Sync(context.Background(), "h_x", &ledgertest.DirectRemote{Server: server, CallerID: "h_c"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range c.ledger.Entries() {
		if e.ID == ownX4.ID {
			t.Fatal("c applied x:4 on top of its forked x:3")
		}
	}
	if got := x.Conflicts(); len(got) != 1 || got[0].Origin != "h_x" || got[0].OriginSeq != 3 {
		t.Fatalf("server conflicts = %+v, want one at h_x:3", got)
	}
}

func TestConflictedOriginIsNotServedBelowTheDetectedSequence(t *testing.T) {
	// Two copies of x forked at 3 and ran on to 10 and 20: comparing heads
	// finds the fork at 10, but 3..9 already differ, so none of x is served.
	b := newMachine("h_b", fixedNow(t0))
	if _, err := b.ledger.ApplyDecisionEntries(context.Background(), "h_x", ledgertest.ReceivedRun("h_x", 1, 10, "dec_1"), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ledger.CheckDecisionHeads(context.Background(), "h_twin", domain.DecisionHeads{"h_x": {Seq: 10, ID: "dent_twin_10"}}, t0); err != nil {
		t.Fatal(err)
	}
	empty := newMachine("h_e", fixedNow(t0))
	empty.syncWith(t, b)
	for _, e := range empty.ledger.Entries() {
		if e.Origin == "h_x" {
			t.Fatalf("empty peer received %s:%d from a quarantined origin", e.Origin, e.OriginSeq)
		}
	}
}
