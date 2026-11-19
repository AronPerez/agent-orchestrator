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

// newDecisionSyncServer serves linked machines' exchanges from the store.
// Entries a peer pushes run wakes (the deliverer and the notification
// projector) and wake waiting exchanges, so they relay on to other peers. The
// server stops its waiting exchanges when ctx ends, so shutdown never waits on
// a held request.
func newDecisionSyncServer(ctx context.Context, store *sqlite.Store, hostID string, log *slog.Logger, wakes ...func()) *decisionsync.Server {
	var server *decisionsync.Server
	server = decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: store, HostID: hostID, MachineLabel: decisionMachineLabel(hostID),
		Peers: store, StatusSource: store, Log: log,
		OnApplied: func(string, domain.DecisionApplyResult) {
			for _, wake := range wakes {
				wake()
			}
			server.Changed()
		},
	})
	go func() {
		<-ctx.Done()
		server.Stop()
	}()
	return server
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
