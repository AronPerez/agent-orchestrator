package decisionsync_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

var errCrash = errors.New("crash")

// forkedFleet is a copied data directory: o and c share h_x. b synced c's
// history first and delivered c's answer to b's own call; d synced only o's.
type forkedFleet struct {
	o, c, b, d *machine
	// delivered is c's answer to dec_b, which b already delivered; pending is
	// c's answer to dec_b2, which b has not.
	delivered, pending string
}

func newForkedFleet(t *testing.T, bOlder bool) *forkedFleet {
	t.Helper()
	f := &forkedFleet{o: newMachine("h_x", nil), b: newMachine("h_b", nil), d: newMachine("h_d", nil)}
	f.b.ledger.NoRepairs = bOlder
	f.o.ledger.Append(ledgertest.Raised("dec_shared", "w-1", "o-label"), t0)
	f.b.ledger.Append(ledgertest.Raised("dec_b", "w-b", "b-label"), t0)
	f.b.ledger.Append(ledgertest.Raised("dec_b2", "w-b", "b-label"), t0)
	f.c = newMachine("h_x", nil)
	if _, err := f.c.ledger.ApplyDecisionEntries(context.Background(), "h_copy", f.o.ledger.Entries(), t0); err != nil {
		t.Fatal(err)
	}
	f.o.ledger.Append(ledgertest.Answer("dec_shared", 1, "from o"), t0.Add(time.Second))
	f.c.syncWith(t, f.b)
	f.c.ledger.Append(ledgertest.Raised("dec_c", "w-c", "c-label"), t0.Add(2*time.Second))
	f.delivered = f.c.ledger.Append(ledgertest.Answer("dec_b", 1, "from c"), t0.Add(3*time.Second)).ID
	f.pending = f.c.ledger.Append(ledgertest.Answer("dec_b2", 2, "from c"), t0.Add(4*time.Second)).ID
	f.c.syncWith(t, f.b)
	f.b.ledger.Append(ledgertest.Delivered("dec_b", "w-b", f.delivered), t0.Add(5*time.Second))
	f.o.syncWith(t, f.b)
	f.o.syncWith(t, f.d)
	f.c.syncWith(t, f.d)
	if !slices.ContainsFunc(f.c.ledger.Conflicts(), func(c domain.DecisionSyncConflict) bool { return c.Origin == "h_x" }) {
		t.Fatalf("the copy never noticed the fork: %+v", f.c.ledger.Conflicts())
	}
	return f
}

func peerOf(m *machine) decisionsync.StoredPeer {
	return decisionsync.StoredPeer{HostID: m.ledger.HostID, URL: "http://" + m.ledger.HostID, Token: "tok"}
}

type repairEnv struct {
	repairer *decisionsync.Repairer
	path     string
	hostID   string
	unlinked []string
	links    []decisionsync.StoredPeer
	ids      int
}

func (e *repairEnv) newID() func(prefix string) string {
	return func(prefix string) string { e.ids++; return fmt.Sprintf("%snew%d", prefix, e.ids) }
}

// repairOn sets up a repairer for m, linked to peers.
func repairOn(t *testing.T, m *machine, peers ...*machine) *repairEnv {
	t.Helper()
	env := &repairEnv{path: filepath.Join(t.TempDir(), "decision-sync", "repair.json"), hostID: m.ledger.HostID}
	byID := map[string]*machine{}
	for _, p := range peers {
		env.links = append(env.links, peerOf(p))
		byID[p.ledger.HostID] = p
	}
	env.repairer = decisionsync.NewRepairer(decisionsync.RepairerDeps{
		Store: m.ledger, HostID: m.ledger.HostID, Path: env.path, Links: staticLinks(env.links),
		Remote: func(p decisionsync.StoredPeer) decisionsync.Remote { return m.remoteTo(byID[p.HostID]) },
		NewID:  env.newID(), Now: fixedNow(t0),
	})
	return env
}

func (e *repairEnv) Linked() []decisionsync.StoredPeer { return slices.Clone(e.links) }

func (e *repairEnv) Unlink(_ context.Context, hostID string) error {
	e.unlinked = append(e.unlinked, hostID)
	e.links = slices.DeleteFunc(e.links, func(p decisionsync.StoredPeer) bool { return p.HostID == hostID })
	return nil
}

// resume finishes a staged repair as a daemon start would, stopping at crash.
func (e *repairEnv) resume(t *testing.T, ledger *ledgertest.MemLedger, crash func(decisionsync.RepairState) error) (decisionsync.RepairRecord, error) {
	t.Helper()
	n := 0
	rec, _, err := decisionsync.ResumeRepair(context.Background(), decisionsync.ResumeDeps{
		Path: e.path, Store: ledger,
		ReadHostID:  func() (string, error) { return e.hostID, nil },
		WriteHostID: func(id string) error { e.hostID = id; return nil },
		NewID:       func() string { n++; return fmt.Sprintf("dent_copy_%d", n) },
		Now:         fixedNow(t0.Add(time.Hour)), AfterStep: crash,
	})
	if err != nil {
		return rec, err
	}
	return decisionsync.FinishRepairLinks(context.Background(), e.path, e, t0.Add(time.Hour))
}

// stageRepair proposes and stages a repair of m's identity against peers.
func stageRepair(t *testing.T, m *machine, peers ...*machine) (*repairEnv, decisionsync.RepairProposal) {
	t.Helper()
	env := repairOn(t, m, peers...)
	proposal, err := env.repairer.Propose(context.Background())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := env.repairer.Stage(context.Background(), proposal.FromSeq, proposal.ForkIDs); err != nil {
		t.Fatalf("stage: %v", err)
	}
	return env, proposal
}

// restartAs is m after a daemon restart under a new identity.
func restartAs(m *machine, hostID string) *machine {
	m.ledger.HostID = hostID
	return newMachineOver(m.ledger)
}

func newMachineOver(ledger *ledgertest.MemLedger) *machine {
	return &machine{
		ledger: ledger,
		engine: decisionsync.NewEngine(decisionsync.EngineDeps{Ledger: ledger, HostID: ledger.HostID}),
		server: decisionsync.NewServer(decisionsync.ServerDeps{Ledger: ledger, HostID: ledger.HostID}),
	}
}

func TestRepairProposesTheCopysOwnEntries(t *testing.T) {
	f := newForkedFleet(t, false)
	env := repairOn(t, f.c, f.b, f.d)
	proposal, err := env.repairer.Propose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// b holds the copy's history and d the original's; only d shows the fork.
	if proposal.Origin != "h_x" || proposal.FromSeq != 2 || len(proposal.ForkIDs) != 3 ||
		!slices.Equal(proposal.CallIDs, []string{"dec_b", "dec_b2", "dec_c"}) || proposal.PeersAsked != 2 || proposal.PeersFailed != 0 {
		t.Fatalf("proposal = %+v", proposal)
	}
}

func TestRepairConverges(t *testing.T) {
	f := newForkedFleet(t, false)
	env, proposal := stageRepair(t, f.c, f.b, f.d)
	rec, err := env.resume(t, f.c.ledger, nil)
	if err != nil || rec.State != decisionsync.RepairDone || !slices.Equal(env.unlinked, []string{"h_b", "h_d"}) {
		t.Fatalf("repair = %+v, %v, unlinked %v", rec, err, env.unlinked)
	}
	if env.hostID != rec.Plan.NewOrigin {
		t.Fatalf("identity = %s, want %s", env.hostID, rec.Plan.NewOrigin)
	}
	archived := f.c.ledger.Archived()
	for _, id := range proposal.ForkIDs {
		if _, ok := archived[id]; !ok {
			t.Fatalf("%s was not moved out on the copy", id)
		}
	}
	c := restartAs(f.c, rec.Plan.NewOrigin)
	quiesce(t, []link{{c, f.b}, {c, f.d}, {f.o, f.b}, {f.o, f.d}})
	assertConverged(t, []*machine{f.o, c, f.b, f.d})

	calls := f.b.ledger.Calls()
	if calls["dec_c"].Origin != rec.Plan.NewOrigin {
		t.Fatalf("the copy's own call now belongs to %s", calls["dec_c"].Origin)
	}
	if got := calls["dec_b"]; got.Delivery != domain.DecisionDeliverySent || got.Redelivery {
		t.Fatalf("an answer already delivered is due again: %+v", got)
	}
	if got := calls["dec_b2"]; got.Delivery != domain.DecisionDeliveryPending || got.Redelivery || got.Option != 2 {
		t.Fatalf("an undelivered answer = %+v, want pending once", got)
	}
	if v, _ := f.b.ledger.DecisionVector(context.Background()); v["h_x"] != 2 {
		t.Fatalf("the original's history did not replace the fork: %v", v)
	}
	for _, m := range []*machine{f.o, f.b, f.d} {
		for _, e := range m.ledger.Entries() {
			if slices.Contains(proposal.ForkIDs, e.ID) {
				t.Fatalf("%s still holds the fork entry %s", m.ledger.HostID, e.ID)
			}
		}
	}
}

func TestRepairLeavesAnOlderMachineQuarantined(t *testing.T) {
	f := newForkedFleet(t, true)
	env, _ := stageRepair(t, f.c, f.b, f.d)
	rec, err := env.resume(t, f.c.ledger, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := restartAs(f.c, rec.Plan.NewOrigin)
	quiesce(t, []link{{c, f.b}, {c, f.d}, {f.o, f.b}, {f.o, f.d}})
	assertConverged(t, []*machine{f.o, c, f.d})
	conflicts := f.b.ledger.Conflicts()
	if len(conflicts) == 0 || slices.ContainsFunc(conflicts, func(c domain.DecisionSyncConflict) bool { return c.Origin != "h_x" }) {
		t.Fatalf("older machine conflicts = %+v, want h_x only", conflicts)
	}
	vb, _ := f.b.ledger.DecisionVector(context.Background())
	vc, _ := c.ledger.DecisionVector(context.Background())
	if vb[rec.Plan.NewOrigin] != vc[rec.Plan.NewOrigin] || vb["h_d"] != vc["h_d"] {
		t.Fatalf("older machine stopped syncing other identities: %v vs %v", vb, vc)
	}
}

func TestRepairRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("NothingRecorded", func(t *testing.T) {
		m, peer := newMachine("h_x", nil), newMachine("h_p", nil)
		if _, err := repairOn(t, m, peer).repairer.Propose(ctx); !errors.Is(err, decisionsync.ErrRepairNothingToDo) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("NoPeerHoldsTheOtherHistory", func(t *testing.T) {
		f := newForkedFleet(t, false)
		if _, err := repairOn(t, f.c, f.b).repairer.Propose(ctx); !errors.Is(err, decisionsync.ErrRepairNeedsPeer) {
			t.Fatalf("copy linked only to a machine with its own history: err = %v", err)
		}
		if _, err := repairOn(t, f.c).repairer.Propose(ctx); !errors.Is(err, decisionsync.ErrRepairNeedsPeer) {
			t.Fatalf("no links: err = %v", err)
		}
	})
	t.Run("OnTheOriginal", func(t *testing.T) {
		f := newForkedFleet(t, false)
		// The original noticed the fork too, through b, but d holds its own history.
		if c, _ := f.o.ledger.CheckDecisionHeads(ctx, "h_b", domain.DecisionHeads{"h_x": {Seq: 2, ID: "dent_copy"}}, t0); len(c) != 1 {
			t.Fatalf("conflict = %+v", c)
		}
		if _, err := repairOn(t, f.o, f.d).repairer.Propose(ctx); !errors.Is(err, decisionsync.ErrRepairNeedsPeer) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ChangedSinceShown", func(t *testing.T) {
		f := newForkedFleet(t, false)
		env := repairOn(t, f.c, f.d)
		proposal, err := env.repairer.Propose(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.repairer.Stage(ctx, proposal.FromSeq, proposal.ForkIDs[1:]); !errors.Is(err, decisionsync.ErrRepairChanged) {
			t.Fatalf("err = %v", err)
		}
		if _, found, _ := env.repairer.Status(); found {
			t.Fatal("a refused stage wrote repair.json")
		}
	})
	t.Run("AlreadyStaged", func(t *testing.T) {
		f := newForkedFleet(t, false)
		env, _ := stageRepair(t, f.c, f.d)
		if _, err := env.repairer.Propose(ctx); !errors.Is(err, decisionsync.ErrRepairPending) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRepairSurvivesACrashAfterEveryStep(t *testing.T) {
	f := newForkedFleet(t, false)
	base := f.c.ledger.Clone()
	run := func(t *testing.T, crashAt decisionsync.RepairState, crashOnWrite bool) (*ledgertest.MemLedger, *repairEnv) {
		t.Helper()
		ledger := base.Clone()
		env, _ := stageRepair(t, newMachineOver(ledger), f.d)
		if crashOnWrite {
			// The identity file was written, then the daemon died.
			rec, _, _ := env.repairer.Status()
			env.hostID = rec.Plan.NewOrigin
		}
		if crashAt != "" {
			_, err := env.resume(t, ledger, func(s decisionsync.RepairState) error {
				if s == crashAt {
					return errCrash
				}
				return nil
			})
			if !errors.Is(err, errCrash) {
				t.Fatalf("crash at %s: err = %v", crashAt, err)
			}
		}
		rec, err := env.resume(t, ledger, nil)
		if err != nil || rec.State != decisionsync.RepairDone {
			t.Fatalf("after restart: %+v, %v", rec, err)
		}
		// Running it again changes nothing.
		before := ledger.Entries()
		if rec, err := env.resume(t, ledger, nil); err != nil || rec.State != decisionsync.RepairDone || len(ledger.Entries()) != len(before) {
			t.Fatalf("second run: %+v, %v", rec, err)
		}
		return ledger, env
	}
	want, wantEnv := run(t, "", false)
	for _, tc := range []struct {
		name    string
		crashAt decisionsync.RepairState
		onWrite bool
	}{
		{"IdentityWritten", "", true},
		{"IdentitySwitched", decisionsync.RepairIdentitySwitched, false},
		{"Reissued", decisionsync.RepairReissued, false},
		{"Archived", decisionsync.RepairArchived, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, env := run(t, tc.crashAt, tc.onWrite)
			ge, we := got.Entries(), want.Entries()
			if len(ge) != len(we) {
				t.Fatalf("%d entries, want %d", len(ge), len(we))
			}
			for i := range ge {
				if !domain.SameDecisionEntry(ge[i], we[i]) {
					t.Fatalf("entry %d = %+v, want %+v", i, ge[i], we[i])
				}
			}
			if got.CallsJSON() != want.CallsJSON() || len(got.Archived()) != len(want.Archived()) || len(got.Conflicts()) != 0 {
				t.Fatalf("ledger differs after a crash at %s", tc.name)
			}
			if env.hostID != wantEnv.hostID || !slices.Equal(env.unlinked, wantEnv.unlinked) {
				t.Fatalf("identity %s unlinked %v, want %s %v", env.hostID, env.unlinked, wantEnv.hostID, wantEnv.unlinked)
			}
		})
	}
	// Once done, the old identity has nothing left to repair.
	if _, err := repairOn(t, newMachineOver(want), f.d).repairer.Propose(context.Background()); !errors.Is(err, decisionsync.ErrRepairNothingToDo) {
		t.Fatalf("after repair: err = %v", err)
	}
}
