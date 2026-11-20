package decisionsync_test

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
	sqlite "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var (
	_ decisionsync.RepairStore = (*ledgertest.MemLedger)(nil)
	_ decisionsync.RepairStore = (*sqlite.Store)(nil)
	_ decisionsync.Ledger      = (*sqlite.Store)(nil)
)
