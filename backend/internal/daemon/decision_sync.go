package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newDecisionSyncServer serves linked machines' exchanges from the store.
// Entries a peer pushes run wakes (the deliverer and the notification
// projector) and wake waiting exchanges, so they relay on to other peers. The
// server stops its waiting exchanges when ctx ends, so shutdown never waits on
// a held request.
func newDecisionSyncServer(ctx context.Context, store *sqlite.Store, decisions *decisionsvc.Service, hostID string, log *slog.Logger, wakes ...func()) *decisionsync.Server {
	var server *decisionsync.Server
	server = decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: gatedSyncLedger{Store: store, decisions: decisions}, HostID: hostID, MachineLabel: decisionMachineLabel(hostID),
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

// gatedSyncLedger is the store as sync sees it, except that received pages
// are applied through the decision service and its plan-first gate.
type gatedSyncLedger struct {
	*sqlite.Store
	decisions *decisionsvc.Service
}

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
