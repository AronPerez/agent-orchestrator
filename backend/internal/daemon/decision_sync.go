package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
	done      <-chan struct{}
}

// startDecisionSync wires sync over the store and starts the scheduler.
// Entries received from any peer run wakes (the deliverer and the
// notification projector), wake waiting exchanges, and poke every other link
// so they relay on. It never blocks the daemon: a peer that is down only
// backs off its own worker.
func startDecisionSync(ctx context.Context, dataDir string, store *sqlite.Store, hostID string, log *slog.Logger, wakes ...func()) *decisionSyncRuntime {
	rt := &decisionSyncRuntime{}
	label := decisionMachineLabel(hostID)
	onApplied := func(peerHostID string, _ domain.DecisionApplyResult) {
		for _, wake := range wakes {
			wake()
		}
		rt.server.Changed()
		rt.scheduler.Poke(peerHostID)
	}
	rt.server = decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: store, HostID: hostID, MachineLabel: label,
		Peers: store, StatusSource: store, Log: log, OnApplied: onApplied,
	})
	secrets, err := decisionsync.LoadPeerSecrets(decisionsync.PeerSecretsPath(dataDir))
	if err != nil {
		log.Warn("decision sync: links file", "err", err)
	}
	client := decisionsync.NewHTTPClient(nil)
	engine := decisionsync.NewEngine(decisionsync.EngineDeps{
		Ledger: store, HostID: hostID, MachineLabel: label, OnApplied: onApplied,
	})
	rt.peers = decisionsync.NewPeers(decisionsync.PeersDeps{
		Secrets: secrets, Store: store, API: client, HostID: hostID, MachineLabel: label,
		OnChange: func() { rt.scheduler.Refresh() },
	})
	rt.scheduler = decisionsync.NewScheduler(decisionsync.SchedulerDeps{
		Engine: engine, Links: rt.peers, Remote: client.Remote, Store: store, Log: log,
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
	rt.done = rt.scheduler.Start(ctx)
	return rt
}

// changed wakes waiting exchanges and pokes every link after a local write.
func (rt *decisionSyncRuntime) changed() {
	rt.server.Changed()
	rt.scheduler.Poke("")
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
