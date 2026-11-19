package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type failingPendingClaimListStore struct {
	*sqlite.Store
}

func (s *failingPendingClaimListStore) ListPendingTrackerIntakeClaims(context.Context) ([]domain.TrackerIntakePendingClaim, error) {
	return nil, errors.New("pending claim list unavailable")
}

// A terminal orchestrator whose host died while AO was down (a reboot, a
// crash, an app quit that stopped its agents) must come back stopped and
// resumable. The daemon runs a reaper tick right after the startup check; that
// tick must not read the startup-found death as a crash and archive the
// orchestrator, which removes it from the project.
func TestStartupHealthKeepsDeadTerminalOrchestratorThroughTheReaper(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	idleSince := now.Add(-time.Hour)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo", Config: testRoleAgents(), RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: idleSince},
		FirstSignalAt: idleSince, CreatedAt: idleSince, UpdatedAt: idleSince,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/orchestrator", Branch: "ao/mer-orchestrator",
			RuntimeHandleID: "ptyhost-v1:mer-1", RuntimeLaunchID: "launch-before-restart",
			AgentSessionID: "agent-x",
		},
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	runtime := &fakeRuntime{} // every handle reads dead, as after a reboot
	lcm := lifecycle.New(st, nil)
	m := New(Deps{
		Runtime:   runtime,
		Agents:    singleAgent{agent: launchArgvAgent{argv: []string{"claude"}}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: lcm,
		DataDir:   t.TempDir(),
	})

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// The daemon's post-reconcile runtime pass.
	if err := reaper.New(lcm, st, runtime, reaper.Config{}).Tick(ctx); err != nil {
		t.Fatalf("reaper tick: %v", err)
	}
	rec, ok, err := st.GetSession(ctx, "mer-1")
	if err != nil || !ok {
		t.Fatalf("read session: ok=%v err=%v", ok, err)
	}
	if rec.IsTerminated || rec.Activity.State != domain.ActivityExited || rec.Metadata.RuntimeReleasedLaunchID != "launch-before-restart" {
		t.Fatalf("orchestrator after startup = terminated:%v activity:%s released:%q; want an exited, released, live session",
			rec.IsTerminated, rec.Activity.State, rec.Metadata.RuntimeReleasedLaunchID)
	}
	if runtime.created != 0 {
		t.Fatalf("startup launched %d runtimes; it must leave the agent stopped", runtime.created)
	}
}

func TestStartupHealthSettlesMatchingTrackerIntakeTerminalBeforeTheReaper(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	idleSince := now.Add(-time.Hour)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo", Config: testRoleAgents(), RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	created, err := st.CreateTrackerIntakeSession(ctx, domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", IssueID: "github:acme/mer#42",
		Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: idleSince},
		FirstSignalAt: idleSince, CreatedAt: idleSince, UpdatedAt: idleSince,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/worker", Branch: "ao/mer-1/root",
			RuntimeHandleID: "ptyhost-v1:mer-1", RuntimeLaunchID: "launch-before-reboot",
			AgentSessionID: "agent-x",
		},
	}, domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2})
	if err != nil {
		t.Fatalf("seed tracker intake session: %v", err)
	}
	if err := st.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		WorkspacePath: created.Metadata.WorkspacePath, RuntimeLaunchID: created.Metadata.RuntimeLaunchID,
		RuntimeHandleIDs: []string{created.Metadata.RuntimeHandleID}, RelaunchPublished: true,
	}); err != nil {
		t.Fatalf("reserve tracker intake owner: %v", err)
	}

	runtime := &fakeRuntime{} // the exact generation disappeared with the reboot
	workspace := &fakeWorkspace{}
	lcm := lifecycle.New(st, nil)
	m := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: launchArgvAgent{argv: []string{"claude"}}},
		Workspace: workspace, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm,
		DataDir: t.TempDir(),
	})
	m.runBackground = func(work func()) { work() }

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := reaper.New(lcm, st, runtime, reaper.Config{}).Tick(ctx); err != nil {
		t.Fatalf("reaper tick: %v", err)
	}
	rec, ok, err := st.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read session: ok=%v err=%v", ok, err)
	}
	if rec.IsTerminated || rec.Activity.State != domain.ActivityExited || !rec.RuntimeReleased() {
		t.Fatalf("tracker intake worker after startup = terminated:%v activity:%s released:%q; want stopped and resumable",
			rec.IsTerminated, rec.Activity.State, rec.Metadata.RuntimeReleasedLaunchID)
	}
	reservation, pending, err := st.GetPendingTrackerIntakeRecovery(ctx, created.ID)
	if err != nil || !pending {
		t.Fatalf("read pending claim: pending=%v err=%v", pending, err)
	}
	if !reservation.CapacityReleased || reservation.ReleasedRuntimeLaunchID != created.Metadata.RuntimeLaunchID {
		t.Fatalf("released reservation = %+v, want exact stopped generation", reservation)
	}
	if runtime.created != 0 || runtime.destroyed != 0 || workspace.destroyed != 0 {
		t.Fatalf("startup created=%d or destroyed runtime=%d workspace=%d", runtime.created, runtime.destroyed, workspace.destroyed)
	}
}

func TestStartupHealthFencesUnresolvedTrackerIntakeTerminalFromTheReaper(t *testing.T) {
	tests := []struct {
		name          string
		claimListFail bool
	}{
		{name: "exact probe inconclusive"},
		{name: "claim list unavailable", claimListFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			base, err := sqlite.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() {
				if err := base.Close(); err != nil {
					t.Errorf("close store: %v", err)
				}
			})
			now := time.Now().UTC().Truncate(time.Millisecond)
			idleSince := now.Add(-time.Hour)
			if err := base.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo", Config: testRoleAgents(), RegisteredAt: now}); err != nil {
				t.Fatalf("seed project: %v", err)
			}
			created, err := base.CreateTrackerIntakeSession(ctx, domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", IssueID: "github:acme/mer#43",
				Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
				Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: idleSince},
				FirstSignalAt: idleSince, CreatedAt: idleSince, UpdatedAt: idleSince,
				Metadata: domain.SessionMetadata{
					WorkspacePath: "/ws/worker", Branch: "ao/mer-1/root",
					RuntimeHandleID: "conpty-v1:mer-1", RuntimeLaunchID: "launch-before-reboot",
					AgentSessionID: "agent-x",
				},
			}, domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2})
			if err != nil {
				t.Fatalf("seed tracker intake session: %v", err)
			}
			if err := base.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
				WorkspacePath: created.Metadata.WorkspacePath, RuntimeLaunchID: created.Metadata.RuntimeLaunchID,
				RuntimeHandleIDs: []string{created.Metadata.RuntimeHandleID}, RelaunchPublished: true,
			}); err != nil {
				t.Fatalf("reserve tracker intake owner: %v", err)
			}

			var managerStore Store = base
			if tt.claimListFail {
				managerStore = &failingPendingClaimListStore{Store: base}
			}
			runtime := &fakeRuntime{
				// Model a refused ConPTY listener: exact ownership remains unknown
				// while the ordinary IsAlive path still reports false, nil.
				fencedResult: ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed},
			}
			lcm := lifecycle.New(base, nil)
			m := New(Deps{
				Runtime: runtime, Agents: singleAgent{agent: launchArgvAgent{argv: []string{"claude"}}},
				Workspace: &fakeWorkspace{}, Store: managerStore, Messenger: &fakeMessenger{}, Lifecycle: lcm,
				DataDir: t.TempDir(),
			})
			m.runBackground = func(work func()) { work() }
			lcm.SetSessionOperationGate(m)

			if err := m.Reconcile(ctx); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if !m.SessionObservationInProgress(created.ID) {
				t.Fatal("unresolved tracker intake owner was not fenced from runtime observations")
			}
			if m.SessionMutationInProgress(created.ID) {
				t.Fatal("reaper-only tracker intake fence blocked explicit session operations")
			}
			if !tt.claimListFail && len(runtime.fencedRefs) < 2 {
				t.Fatalf("exact ownership probes = %d, want startup probe plus scheduled retry", len(runtime.fencedRefs))
			}
			if err := reaper.New(lcm, base, runtime, reaper.Config{}).Tick(ctx); err != nil {
				t.Fatalf("reaper tick: %v", err)
			}
			rec, ok, err := base.GetSession(ctx, created.ID)
			if err != nil || !ok {
				t.Fatalf("read session: ok=%v err=%v", ok, err)
			}
			if rec.IsTerminated || rec.Activity.State != domain.ActivityIdle || rec.RuntimeReleased() {
				t.Fatalf("unresolved terminal after reaper = terminated:%v activity:%s released:%v; want unchanged live row",
					rec.IsTerminated, rec.Activity.State, rec.RuntimeReleased())
			}
		})
	}
}
