package daemon

import (
	"log/slog"
	"testing"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestNewTelemetrySink_DefaultsToNoopWhenDisabled(t *testing.T) {
	sink := newTelemetrySink(config.Config{}, nil, slog.Default())
	if _, ok := sink.(telemetryadapter.NoopSink); !ok {
		t.Fatalf("sink type = %T, want telemetry.NoopSink", sink)
	}
}

func TestNewTelemetrySink_UsesLocalSQLiteWhenEnabled(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink := newTelemetrySink(config.Config{LocalEvents: true, DataDir: dataDir}, store, slog.Default())
	local, ok := sink.(*telemetryadapter.LocalSQLiteSink)
	if !ok {
		t.Fatalf("sink type = %T, want *telemetry.LocalSQLiteSink", sink)
	}
	t.Cleanup(func() { _ = local.Close(t.Context()) })
}
