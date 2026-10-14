package daemon

import (
	"log/slog"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newTelemetrySink only writes opt-in diagnostics to the local database.
func newTelemetrySink(cfg config.Config, store *sqlite.Store, log *slog.Logger) ports.EventSink {
	if !cfg.LocalEvents {
		return telemetryadapter.NoopSink{}
	}
	return telemetryadapter.NewLocalSQLiteSink(store, log)
}
