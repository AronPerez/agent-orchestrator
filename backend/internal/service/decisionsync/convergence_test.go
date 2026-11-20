package decisionsync_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

var convergenceSeeds = flag.Int("decisionsync.seeds", 200, "seeds for TestConvergenceRandomized")

var errCut = errors.New("connection cut")

// link is one configured sync link: from initiates, to serves.
type link struct{ from, to *machine }

// syncPaged runs one sync over l with the given page size, optionally cut
// after cutAfter calls (0 = never).
func syncPaged(l link, page, cutAfter int) (decisionsync.Outcome, error) {
	from, to := l.from, l.to
	engine := decisionsync.NewEngine(decisionsync.EngineDeps{
		Ledger: from.ledger, HostID: from.ledger.HostID, PageEntries: page,
		OnApplied: func(string, domain.DecisionApplyResult) { from.applied++ },
	})
	server := decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: to.ledger, HostID: to.ledger.HostID, PageEntries: page,
		OnApplied: func(string, domain.DecisionApplyResult) { to.applied++ },
	})
	remote := &ledgertest.DirectRemote{Server: server, CallerID: from.ledger.HostID}
	if cutAfter > 0 {
		remote.BeforeCall = func(n int) error {
			if n > cutAfter {
				return errCut
			}
			return nil
		}
	}
	return engine.Sync(context.Background(), to.ledger.HostID, remote)
}

// quiesce syncs every link, round after round, until a whole round moves
// nothing.
func quiesce(t *testing.T, links []link) {
	t.Helper()
	for round := 0; round < 20; round++ {
		moved := 0
		for _, l := range links {
			out, err := syncPaged(l, 0, 0)
			if err != nil {
				t.Fatalf("quiesce %s→%s: %v", l.from.ledger.HostID, l.to.ledger.HostID, err)
			}
			moved += out.Pulled + out.Pushed
		}
		if moved == 0 {
			return
		}
	}
	t.Fatal("links did not quiesce within 20 rounds")
}

// assertConverged checks every machine holds byte-identical entries, equal
// vectors and equal reduced calls, with no conflicts, and that only a call's
// owning machine delivered it.
func assertConverged(t *testing.T, machines []*machine) {
	t.Helper()
	ctx := context.Background()
	ref := machines[0].ledger
	refEntries := ref.Entries()
	refVector, _ := ref.DecisionVector(ctx)
	refCalls := ref.CallsJSON()
	for _, m := range machines {
		entries := m.ledger.Entries()
		if len(entries) != len(refEntries) {
			t.Fatalf("%s holds %d entries, %s holds %d", m.ledger.HostID, len(entries), ref.HostID, len(refEntries))
		}
		for i := range entries {
			if !domain.SameDecisionEntry(entries[i], refEntries[i]) {
				t.Fatalf("%s entry %d = %+v, want %+v", m.ledger.HostID, i, entries[i], refEntries[i])
			}
		}
		if v, _ := m.ledger.DecisionVector(ctx); !v.Equal(refVector) {
			t.Fatalf("%s vector %v, want %v", m.ledger.HostID, v, refVector)
		}
		if calls := m.ledger.CallsJSON(); calls != refCalls {
			t.Fatalf("%s calls differ:\n%s\nwant\n%s", m.ledger.HostID, calls, refCalls)
		}
		if c := m.ledger.Conflicts(); len(c) != 0 {
			t.Fatalf("%s recorded conflicts %+v", m.ledger.HostID, c)
		}
	}
	calls := ref.Calls()
	for _, e := range refEntries {
		if e.Kind != domain.DecisionKindDelivered {
			continue
		}
		if call, ok := calls[e.CallID]; ok && call.Origin != e.Origin {
			t.Fatalf("delivered entry %s written by %s for a call owned by %s", e.ID, e.Origin, call.Origin)
		}
	}
}

func TestConvergenceLine(t *testing.T) {
	a, b, c := newMachine("h_a", fixedNow(t0)), newMachine("h_b", fixedNow(t0)), newMachine("h_c", fixedNow(t0))
	a.ledger.Append(ledgertest.Raised("dec_a", "w-a", "a"), t0)
	c.ledger.Append(ledgertest.Raised("dec_c", "w-c", "c"), t0.Add(time.Second))
	b.ledger.Append(ledgertest.Answer("dec_a", 1, ""), t0.Add(2*time.Second))
	quiesce(t, []link{{a, b}, {b, c}})
	assertConverged(t, []*machine{a, b, c})
	if len(a.ledger.Calls()) != 2 {
		t.Fatalf("a calls = %d, want 2 (c's call relayed through b)", len(a.ledger.Calls()))
	}
}

func TestConvergenceRandomized(t *testing.T) {
	topologies := map[string]func(ms []*machine) []link{
		"line":   func(ms []*machine) []link { return []link{{ms[0], ms[1]}, {ms[1], ms[2]}} },
		"star":   func(ms []*machine) []link { return []link{{ms[0], ms[3]}, {ms[1], ms[3]}, {ms[2], ms[3]}} },
		"ring":   func(ms []*machine) []link { return []link{{ms[0], ms[1]}, {ms[1], ms[2]}, {ms[2], ms[0]}} },
		"oneway": func(ms []*machine) []link { return []link{{ms[0], ms[1]}} },
	}
	sizes := map[string]int{"line": 3, "star": 4, "ring": 3, "oneway": 2}
	for name, build := range topologies {
		for seed := 1; seed <= *convergenceSeeds; seed++ {
			t.Run(fmt.Sprintf("%s/seed=%d", name, seed), func(t *testing.T) {
				rng := rand.New(rand.NewSource(int64(seed)))
				var machines []*machine
				for i := 0; i < sizes[name]; i++ {
					machines = append(machines, newMachine(fmt.Sprintf("h_%c", 'a'+i), nil))
				}
				links := build(machines)
				runRandomOps(t, rng, machines, links, 40)
				quiesce(t, links)
				assertConverged(t, machines)
			})
		}
	}
}

// runRandomOps performs steps random writes and syncs. Each machine's clock
// runs with its own skew of up to ±3 seconds.
func runRandomOps(t *testing.T, rng *rand.Rand, machines []*machine, links []link, steps int) {
	t.Helper()
	skew := make([]time.Duration, len(machines))
	for i := range skew {
		skew[i] = time.Duration(rng.Intn(6000)-3000) * time.Millisecond
	}
	clock := t0
	nextCall := 0
	for step := 0; step < steps; step++ {
		clock = clock.Add(time.Duration(rng.Intn(2000)) * time.Millisecond)
		i := rng.Intn(len(machines))
		m := machines[i]
		now := clock.Add(skew[i])
		owner := domain.SessionID("w-" + m.ledger.HostID)
		calls := m.ledger.Calls()
		ids := make([]string, 0, len(calls))
		for id := range calls {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		pick := func() (domain.DecisionCall, bool) {
			if len(ids) == 0 {
				return domain.DecisionCall{}, false
			}
			return calls[ids[rng.Intn(len(ids))]], true
		}
		switch op := rng.Intn(14); {
		case op < 2:
			nextCall++
			m.ledger.Append(ledgertest.Raised(fmt.Sprintf("dec_%d", nextCall), owner, m.ledger.HostID), now)
		case op == 2:
			nextCall++
			m.ledger.Append(ledgertest.Decided(fmt.Sprintf("dec_%d", nextCall), owner, m.ledger.HostID), now)
		case op == 3 || op == 4:
			if call, ok := pick(); ok {
				m.ledger.Append(ledgertest.Answer(call.ID, 1+rng.Intn(2), "pick"), now)
			}
		case op == 5:
			if call, ok := pick(); ok {
				m.ledger.Append(ledgertest.Dismiss(call.ID), now)
			}
		case op == 6:
			if call, ok := pick(); ok {
				m.ledger.Append(ledgertest.Retire(call.ID, rng.Intn(2) == 0), now)
			}
		case op == 7:
			if call, ok := pick(); ok {
				m.ledger.Append(ledgertest.Overrule(call.ID), now)
			}
		case op == 8:
			if call, ok := pick(); ok && call.Origin == m.ledger.HostID {
				m.ledger.Append(ledgertest.Outcome(call.ID, call.Raised.OwnerSessionID), now)
			}
		case op == 9:
			if call, ok := pick(); ok && call.Origin == m.ledger.HostID {
				m.ledger.Append(ledgertest.Withdraw(call.ID, call.Raised.OwnerSessionID), now)
			}
		case op == 10:
			if call, ok := pick(); ok && call.Origin == m.ledger.HostID {
				for _, e := range m.ledger.Entries() {
					if e.CallID == call.ID && e.Kind == domain.DecisionKindDecided {
						m.ledger.Append(ledgertest.CarriedOut(call.ID, call.Raised.OwnerSessionID, e.ID), now)
					}
				}
			}
		case op == 11:
			// The owning machine's deliverer, never another machine's.
			for _, id := range ids {
				call := calls[id]
				if call.Origin == m.ledger.HostID && call.Delivery == domain.DecisionDeliveryPending {
					m.ledger.Append(ledgertest.Delivered(call.ID, call.Raised.OwnerSessionID, call.StandingEntryID), now)
				}
			}
		case op == 12:
			if call, ok := pick(); ok {
				m.ledger.Append(ledgertest.Future(call.ID), now)
			}
		default:
			l := links[rng.Intn(len(links))]
			cut := 0
			if rng.Intn(3) == 0 {
				cut = 1 + rng.Intn(3)
			}
			if _, err := syncPaged(l, 1+rng.Intn(7), cut); err != nil && !errors.Is(err, errCut) {
				t.Fatalf("sync %s→%s: %v", l.from.ledger.HostID, l.to.ledger.HostID, err)
			}
		}
	}
}

func TestConvergenceAnswersCross(t *testing.T) {
	a, b := newMachine("h_a", nil), newMachine("h_b", nil)
	a.ledger.Append(ledgertest.Raised("dec_1", "w-a", "a"), t0)
	quiesce(t, []link{{a, b}})
	first := a.ledger.Append(ledgertest.Answer("dec_1", 1, "a says"), t0.Add(10*time.Second))
	second := b.ledger.Append(ledgertest.Answer("dec_1", 2, "b says"), t0.Add(20*time.Second))
	quiesce(t, []link{{a, b}})
	assertConverged(t, []*machine{a, b})
	for _, m := range []*machine{a, b} {
		call := m.ledger.Calls()["dec_1"]
		if call.StandingEntryID != second.ID || call.Option != 2 {
			t.Fatalf("%s standing = %s option %d, want the later answer %s", m.ledger.HostID, call.StandingEntryID, call.Option, second.ID)
		}
	}
	var kept int
	for _, e := range a.ledger.Entries() {
		if e.ID == first.ID || e.ID == second.ID {
			kept++
		}
	}
	if kept != 2 {
		t.Fatalf("answers kept = %d, want both", kept)
	}
	if call := a.ledger.Calls()["dec_1"]; call.Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("owner delivery = %s, want pending for the owner's deliverer", call.Delivery)
	}
}

func TestConvergenceAnswerAfterOutcome(t *testing.T) {
	a, b := newMachine("h_a", nil), newMachine("h_b", nil)
	a.ledger.Append(ledgertest.Raised("dec_1", "w-a", "a"), t0)
	quiesce(t, []link{{a, b}})
	answer := b.ledger.Append(ledgertest.Answer("dec_1", 1, ""), t0.Add(time.Second))
	quiesce(t, []link{{a, b}})
	a.ledger.Append(ledgertest.Delivered("dec_1", "w-a", answer.ID), t0.Add(2*time.Second))
	a.ledger.Append(ledgertest.Outcome("dec_1", "w-a"), t0.Add(3*time.Second))
	b.ledger.Append(ledgertest.Answer("dec_1", 2, "changed"), t0.Add(5*time.Second))
	quiesce(t, []link{{a, b}})
	assertConverged(t, []*machine{a, b})
	for _, m := range []*machine{a, b} {
		call := m.ledger.Calls()["dec_1"]
		if call.State != domain.DecisionStateDone || !call.ChangedAfterClose {
			t.Fatalf("%s state = %s changedAfterClose = %v, want done and true", m.ledger.HostID, call.State, call.ChangedAfterClose)
		}
	}
}

func TestConvergenceUnknownKindRelayedVerbatim(t *testing.T) {
	a, b, c := newMachine("h_a", nil), newMachine("h_b", nil), newMachine("h_c", nil)
	written := a.ledger.Append(ledgertest.Future("dec_1"), t0)
	quiesce(t, []link{{a, b}})
	quiesce(t, []link{{c, b}})
	for _, e := range c.ledger.Entries() {
		if e.ID == written.ID {
			if string(e.Payload) != string(written.Payload) {
				t.Fatalf("c payload = %q, want %q", e.Payload, written.Payload)
			}
			return
		}
	}
	t.Fatal("c never received the unknown entry")
}

func TestConvergenceForkStopsOneOrigin(t *testing.T) {
	a, b, c := newMachine("h_a", nil), newMachine("h_b", nil), newMachine("h_c", nil)
	a.ledger.Append(ledgertest.Future("dec_1"), t0)
	a.ledger.Append(ledgertest.Future("dec_1"), t0.Add(time.Second))
	// a′ is a copy of a's data directory: same identity, same first entries.
	twin := newMachine("h_a", nil)
	if _, err := twin.ledger.ApplyDecisionEntries(context.Background(), "h_copy", a.ledger.Entries(), t0); err != nil {
		t.Fatal(err)
	}
	a.ledger.Append(ledgertest.Future("dec_1"), t0.Add(2*time.Second))
	twin.ledger.Append(ledgertest.Future("dec_1"), t0.Add(2*time.Second))
	c.ledger.Append(ledgertest.Future("dec_c"), t0.Add(3*time.Second))
	links := []link{{a, b}, {twin, b}, {c, b}}
	for _, l := range links {
		if _, err := syncPaged(l, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.ledger.Conflicts(); len(got) != 1 || got[0].Origin != "h_a" || got[0].OriginSeq != 3 {
		t.Fatalf("b conflicts = %+v, want one at h_a:3", got)
	}
	a.ledger.Append(ledgertest.Future("dec_1"), t0.Add(4*time.Second))
	c.ledger.Append(ledgertest.Future("dec_c"), t0.Add(5*time.Second))
	for _, l := range links {
		if _, err := syncPaged(l, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	v, _ := b.ledger.DecisionVector(context.Background())
	if v["h_a"] != 3 || v["h_c"] != 2 {
		t.Fatalf("b vector = %v, want h_a stopped at 3 and h_c still syncing to 2", v)
	}
	if got := b.ledger.Conflicts(); len(got) != 1 {
		t.Fatalf("b conflicts = %+v, want still one", got)
	}
	if _, err := syncPaged(link{a, b}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if va, _ := a.ledger.DecisionVector(context.Background()); va["h_c"] != 2 {
		t.Fatalf("a vector = %v, want h_c relayed", va)
	}
}

func TestConvergencePlanAnswerOnSupersededCall(t *testing.T) {
	a, b := newMachine("h_a", nil), newMachine("h_b", nil)
	a.ledger.Append(ledgertest.PlanRaised("dec_p1", "w-a", "a"), t0)
	quiesce(t, []link{{a, b}})
	// The owner replaces its plan call; b has not heard and answers the old one.
	a.ledger.Append(ledgertest.Withdraw("dec_p1", "w-a"), t0.Add(time.Second))
	a.ledger.Append(ledgertest.PlanRaised("dec_p2", "w-a", "a"), t0.Add(2*time.Second))
	stale := b.ledger.Append(ledgertest.Answer("dec_p1", 1, "go"), t0.Add(3*time.Second))
	quiesce(t, []link{{a, b}})
	// The owner's deliverer finds a newer plan call for the owner and closes
	// the stale answer as superseded instead of delivering it.
	sup := ledgertest.Delivered("dec_p1", "w-a", stale.ID)
	sup.Payload = []byte(`{"forEntry":"` + stale.ID + `","status":"superseded"}`)
	a.ledger.Append(sup, t0.Add(4*time.Second))
	quiesce(t, []link{{a, b}})
	assertConverged(t, []*machine{a, b})
	for _, m := range []*machine{a, b} {
		calls := m.ledger.Calls()
		if calls["dec_p2"].State != domain.DecisionStateNeedsYou {
			t.Fatalf("%s newer plan call state = %s, want needs_you", m.ledger.HostID, calls["dec_p2"].State)
		}
	}
}
