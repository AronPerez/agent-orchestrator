package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	trackerintake "github.com/aoagents/agent-orchestrator/backend/internal/observe/trackerintake"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type trackerIntakeSessions interface {
	trackerintake.Spawner
	ScheduleTrackerIntakeClaimReconciliation(context.Context) error
}

// startTrackerIntake starts the intake observer when cfg.TrackerIntake is on.
// The observer then runs unconditionally: Poll re-reads each project's config
// every tick, so a project enabling intake after boot is picked up without a
// restart. When admission is off, pending-claim recovery still runs so an
// inconclusive startup probe does not fence a session for the daemon lifetime.
func startTrackerIntake(ctx context.Context, cfg config.Config, store *sqlite.Store, sessions trackerIntakeSessions, tracker ports.Tracker, logger *slog.Logger, startGate <-chan struct{}) <-chan struct{} {
	return startTrackerIntakeWithRecoveryTick(ctx, cfg, store, sessions, tracker, logger, startGate, trackerintake.DefaultTickInterval)
}

func startTrackerIntakeWithRecoveryTick(ctx context.Context, cfg config.Config, store *sqlite.Store, sessions trackerIntakeSessions, tracker ports.Tracker, logger *slog.Logger, startGate <-chan struct{}, recoveryTick time.Duration) <-chan struct{} {
	if !cfg.TrackerIntake {
		logGatedOffIntake(ctx, store, logger)
		return startTrackerIntakeRecovery(ctx, sessions, logger, startGate, recoveryTick)
	}
	// SingleTrackerResolver with an empty Provider matches any provider,
	// letting the multi-tracker dispatch based on each project's configured
	// provider. A nil tracker (no credentials) causes Resolve to return an
	// error for every project, which triggers backoff — correct behavior.
	resolver := trackerintake.SingleTrackerResolver{
		Adapter: tracker,
	}
	observer := trackerintake.New(resolver, store, sessions, trackerintake.Config{Logger: logger, StartGate: startGate})
	return observer.Start(ctx)
}

func startTrackerIntakeRecovery(ctx context.Context, sessions trackerIntakeSessions, logger *slog.Logger, startGate <-chan struct{}, tick time.Duration) <-chan struct{} {
	if sessions == nil {
		return nil
	}
	if tick <= 0 {
		tick = trackerintake.DefaultTickInterval
	}
	start := func() <-chan struct{} {
		return observe.StartPollLoop(ctx, tick, sessions.ScheduleTrackerIntakeClaimReconciliation, logger, "tracker intake recovery")
	}
	if startGate == nil {
		return start()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		case <-startGate:
		}
		<-start()
	}()
	return done
}

func logGatedOffIntake(ctx context.Context, store *sqlite.Store, logger *slog.Logger) {
	const hint = "tracker intake: gated off, set AO_TRACKER_INTAKE=on to enable"
	projects, err := store.ListProjects(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// Boot was interrupted; the scan failed because the daemon is
			// shutting down, not because projects are unreadable.
			return
		}
		logger.Warn(hint, "projectScanErr", err)
		return
	}
	stranded := 0
	for _, project := range projects {
		if project.Config.TrackerIntake.Enabled {
			stranded++
		}
	}
	if stranded == 0 {
		logger.Info(hint)
		return
	}
	logger.Warn(hint, "projectsWithIntakeEnabled", stranded)
}
