package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// decisionSyncRuntime is decision ledger sync as the daemon runs it: the
// server peers exchange with, the scheduler that initiates exchanges with
// linked machines, and the link manager behind the person's routes.
type decisionSyncRuntime struct {
	server    *decisionsync.Server
	scheduler *decisionsync.Scheduler
	peers     *decisionsync.Peers
	repairer  *decisionsync.Repairer
	done      <-chan struct{}
	// stopScheduler ends every link's worker; pause uses it.
	stopScheduler context.CancelFunc
}

// pause stops sync in both directions until the daemon restarts: a staged
// identity repair must not let any peer see the old identity again.
func (rt *decisionSyncRuntime) pause() {
	rt.server.Pause()
	rt.stopScheduler()
}

// resumeDecisionRepair finishes the local steps of a staged identity repair:
// switch the host identity, re-issue the fork under it, and move the
// originals out of the log. It runs before the daemon reads its identity, so
// everything after starts under the new one. On failure the repair stays
// staged and sync stays off; the next start tries again.
func resumeDecisionRepair(ctx context.Context, dataDir string, store *sqlite.Store, log *slog.Logger) {
	identity := mobilebridge.IdentityPath(dataDir)
	rec, found, err := decisionsync.ResumeRepair(ctx, decisionsync.ResumeDeps{
		Path: decisionsync.RepairPath(dataDir), Store: store,
		ReadHostID:  func() (string, error) { return mobilebridge.ReadHostID(identity) },
		WriteHostID: func(id string) error { return mobilebridge.ReplaceHostID(identity, id) },
		NewID:       func() string { return "dent_" + uuid.NewString() },
	})
	switch {
	case err != nil:
		log.Error("decision identity repair did not finish; decision sync stays off until it does", "state", rec.State, "err", err)
	case found && rec.SyncHeld():
		log.Info("decision identity repair: identity switched and entries re-issued", "from", rec.Plan.Origin, "to", rec.Plan.NewOrigin)
	}
}

// finishDecisionRepairLinks removes the outbound links of a repair whose
// local steps are done, and reports whether sync must stay off.
func finishDecisionRepairLinks(ctx context.Context, dataDir string, peers *decisionsync.Peers, log *slog.Logger) bool {
	path := decisionsync.RepairPath(dataDir)
	rec, found, err := decisionsync.LoadRepair(path)
	if err == nil && (!found || !rec.SyncHeld()) {
		return false
	}
	if err == nil && rec.State == decisionsync.RepairArchived {
		rec, err = decisionsync.FinishRepairLinks(ctx, path, peers, time.Now())
	}
	if err != nil || rec.SyncHeld() {
		log.Warn("decision sync stays off: an identity repair has not finished", "state", rec.State, "err", err)
		return true
	}
	log.Info("decision identity repair finished; this machine's links were removed and must be added again", "identity", rec.Plan.NewOrigin)
	return false
}

// startDecisionSync wires sync over the store and starts the scheduler.
// Entries received from any peer run wakes (the deliverer and the
// notification projector), wake waiting exchanges, and poke every other link
// so they relay on. It never blocks the daemon: a peer that is down only
// backs off its own worker.
func startDecisionSync(ctx context.Context, dataDir string, store *sqlite.Store, decisions *decisionsvc.Service, hostID string, issued decisionsync.IssuedCredentials, log *slog.Logger, wakes ...func()) *decisionSyncRuntime {
	rt := &decisionSyncRuntime{}
	// Received pages, pulled or pushed, go through the decision service so a
	// synced answer passes the same plan-first gate as a local one.
	ledger := gatedSyncLedger{Store: store, decisions: decisions}
	label := decisionMachineLabel(hostID)
	onApplied := func(peerHostID string, _ domain.DecisionApplyResult) {
		for _, wake := range wakes {
			wake()
		}
		rt.server.Changed()
		rt.scheduler.Poke(peerHostID)
	}
	rt.server = decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: ledger, HostID: hostID, MachineLabel: label,
		Peers: store, StatusSource: store, Log: log, OnApplied: onApplied,
	})
	secrets, err := decisionsync.LoadPeerSecrets(decisionsync.PeerSecretsPath(dataDir))
	if err != nil {
		log.Warn("decision sync: links file", "err", err)
	}
	client := decisionsync.NewHTTPClient(nil)
	engine := decisionsync.NewEngine(decisionsync.EngineDeps{
		Ledger: ledger, HostID: hostID, MachineLabel: label, OnApplied: onApplied,
	})
	rt.peers = decisionsync.NewPeers(decisionsync.PeersDeps{
		Secrets: secrets, Store: store, API: client, Issued: issued, HostID: hostID, MachineLabel: label,
		OnChange: func() { rt.scheduler.Refresh() },
	})
	rt.scheduler = decisionsync.NewScheduler(decisionsync.SchedulerDeps{
		Engine: engine, Links: rt.peers, Remote: client.Remote, Store: store, Log: log,
	})
	schedulerCtx, stopScheduler := context.WithCancel(ctx)
	rt.stopScheduler = stopScheduler
	rt.repairer = decisionsync.NewRepairer(decisionsync.RepairerDeps{
		Store: store, HostID: hostID, MachineLabel: label, Path: decisionsync.RepairPath(dataDir),
		Links: rt.peers, Remote: client.Remote, Pause: rt.pause,
		NewID: func(prefix string) string { return prefix + uuid.NewString() },
	})
	if gaps, err := store.AuditDecisionOrigins(ctx); err != nil {
		log.Warn("decision sync: audit origins", "err", err)
	} else {
		for _, gap := range gaps {
			log.Warn("decision sync: origin has holes", "origin", gap.Origin, "held", gap.Have, "highest", gap.Got)
		}
	}
	go func() {
		<-ctx.Done()
		rt.server.Stop()
	}()
	if finishDecisionRepairLinks(ctx, dataDir, rt.peers, log) {
		rt.pause()
		done := make(chan struct{})
		close(done)
		rt.done = done
		return rt
	}
	rt.done = rt.scheduler.Start(schedulerCtx)
	return rt
}

// changed wakes waiting exchanges and pokes every link after a local write.
func (rt *decisionSyncRuntime) changed() {
	rt.server.Changed()
	rt.scheduler.Poke("")
}

// gatedSyncLedger is the store as sync sees it, except that received pages
// are applied through the decision service and its plan-first gate.
type gatedSyncLedger struct {
	*sqlite.Store
	decisions *decisionsvc.Service
}

// GuardsApply marks the ledger as running only its store write inside the
// sync request's guard, after the plan gates (decisionsync.SelfGuardedLedger).
func (gatedSyncLedger) GuardsApply() {}

func (l gatedSyncLedger) ApplyDecisionEntries(ctx context.Context, peerHostID string, entries []domain.DecisionEntry, now time.Time) (domain.DecisionApplyResult, error) {
	return l.decisions.ApplySyncedEntries(ctx, peerHostID, entries, now)
}

// decisionMachineLabel names this machine the way the decision service does:
// its hostname, or its host id when that is unavailable.
func decisionMachineLabel(hostID string) func() string {
	return func() string {
		label, err := os.Hostname()
		if err != nil || strings.TrimSpace(label) == "" {
			return hostID
		}
		return label
	}
}
