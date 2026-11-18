package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
		for _, base := range []string{"", "feat/parent-base"} {
			t.Run(tc.name+"/base="+base, func(t *testing.T) {
				m, st, _, ws := newManager()
				if tc.parent.ID != "" {
					st.sessions[tc.parent.ID] = tc.parent
				}
				rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "parent", BaseBranch: base, AgentConfig: ports.AgentConfig{Permissions: ports.PermissionModeAuto}})
				if err != nil {
					t.Fatal(err)
				}
				if ws.lastCfg.BaseBranch != base {
					t.Fatalf("base branch=%q", ws.lastCfg.BaseBranch)
				}
				if rec.ParentSessionID != tc.want {
					t.Fatalf("parent=%q want=%q", rec.ParentSessionID, tc.want)
				}
			})
		}
	}
}

func TestSpawnRemoteParentLineageNeverResolvesSameNamedLocalSession(t *testing.T) {
	m, st, rt, _ := newManager()
	st.sessions["parent"] = domain.SessionRecord{ID: "parent", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.conversations["parent"] = domain.ConversationRecord{
		SessionID: "parent", Settings: domain.ConversationSettings{ApprovalMode: domain.PermissionModeBypassPermissions},
	}
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, ParentHostID: "h_source", ParentSessionID: "parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ParentHostID != "h_source" || rec.ParentSessionID != "parent" {
		t.Fatalf("remote parent = %q/%q, want h_source/parent", rec.ParentHostID, rec.ParentSessionID)
	}
	if got := rt.lastCfg.Env[EnvPermissionMode]; got != string(domain.PermissionModeAuto) {
		t.Fatalf("remote parent inherited colliding local permissions %q, want project default %q", got, domain.PermissionModeAuto)
	}
}
