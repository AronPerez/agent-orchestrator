package ledgertest_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

func TestMemLedgerConformance(t *testing.T) {
	ledgertest.RunApplyConformance(t, func(_ *testing.T, hostID string) decisionsync.Ledger {
		return ledgertest.NewMemLedger(hostID)
	})
}
