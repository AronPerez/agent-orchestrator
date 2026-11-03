package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"testing"
)

func TestSpawnParentLineageWithExplicitPermissions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent domain.SessionRecord
		want   domain.SessionID
	}{
		{"worker manager", domain.SessionRecord{ID: "parent", ProjectID: "mer", Kind: domain.KindWorker}, "parent"},
		{"orchestrator", domain.SessionRecord{ID: "parent", ProjectID: "mer", Kind: domain.KindOrchestrator}, "parent"},
		{"cross project", domain.SessionRecord{ID: "parent", ProjectID: "other", Kind: domain.KindWorker}, ""},
		{"missing", domain.SessionRecord{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			if tc.parent.ID != "" {
				st.sessions[tc.parent.ID] = tc.parent
			}
			rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "parent", AgentConfig: ports.AgentConfig{Permissions: ports.PermissionModeAuto}})
			if err != nil {
				t.Fatal(err)
			}
			if rec.ParentSessionID != tc.want {
				t.Fatalf("parent=%q want=%q", rec.ParentSessionID, tc.want)
			}
		})
	}
}
