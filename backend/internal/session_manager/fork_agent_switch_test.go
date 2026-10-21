package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func (s *fakeStore) ReserveForkSwitchLaunch(_ context.Context, rec domain.SessionRecord, launchID string) (bool, error) {
	current := s.sessions[rec.ID]
	if current.Revision != rec.Revision || current.Harness != rec.Harness || current.IsTerminated != rec.IsTerminated || current.Metadata.RuntimeHandleID != "" {
		return false, nil
	}
	current.Metadata.RuntimeLaunchID, current.UpdatedAt = launchID, rec.UpdatedAt
	current.Revision++
	s.sessions[rec.ID] = current
	return true, nil
}

func TestForkRecoverSwitchOwnerVerifiesLiveTerminalAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		runtime  *fakeRuntime
		expected domain.AgentHarness
		wantErr  bool
	}{
		{"exact live source", &fakeRuntime{aliveByHandle: map[string]bool{"handle": true}}, domain.HarnessClaudeCode, false},
		{"probe error", &fakeRuntime{aliveErr: errors.New("probe unavailable"), supervisedErr: errors.New("probe unavailable")}, domain.HarnessClaudeCode, true},
		{"owner changed", &fakeRuntime{}, domain.HarnessCodex, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", RuntimeHandleID: "handle", RuntimeLaunchID: "launch"})
			rec := st.sessions["mer-1"]
			rec.IsTerminated = false
			rec.Harness = domain.HarnessClaudeCode
			st.sessions["mer-1"] = rec
			m := New(Deps{Runtime: tc.runtime, Store: st, Lifecycle: &fakeLCM{store: st}})
			err := m.RecoverForkSwitchOwner(context.Background(), "mer-1", tc.expected)
			if (err != nil) != tc.wantErr {
				t.Fatalf("recovery error=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.runtime.created != 0 || tc.runtime.destroyed != 0 {
				t.Fatal("verification replaced a possibly-live agent")
			}
		})
	}
}

func TestForkSwitchWorkersUseDaemonShutdownFence(t *testing.T) {
	m := New(Deps{})
	started, release := make(chan struct{}), make(chan struct{})
	if err := m.RunForkSwitch(func(context.Context) { close(started); <-release }); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.WaitAgentSwitchWorkers(ctx); err == nil {
		t.Fatal("shutdown ignored active switch")
	}
	if err := m.RunForkSwitch(func(context.Context) { t.Error("work admitted during shutdown") }); err == nil {
		t.Fatal("admitted worker after shutdown fence")
	}
	close(release)
	if err := m.WaitAgentSwitchWorkers(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestForkRecoverSwitchOwnerUsesNativeResumeForProvenDeadSource(t *testing.T) {
	runtime := &fakeRuntime{}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, st, _ := newExitedResumeManager(t, runtime, agent)
	if err := m.RecoverForkSwitchOwner(context.Background(), "mer-1", domain.HarnessCodex); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if runtime.created != 1 || got.Metadata.RuntimeLaunchID != "launch-new" || got.Metadata.AgentSessionID != "agent-x" || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("recovery did not resume native source: created=%d %+v", runtime.created, got)
	}
}

type forkOwnerCommitCrashStore struct {
	*sqlite.Store
	crash bool
}

func (s *forkOwnerCommitCrashStore) UpdateSessionWithLaunchOptions(ctx context.Context, rec domain.SessionRecord, options domain.AgentLaunchOptions) error {
	if err := s.Store.UpdateSessionWithLaunchOptions(ctx, rec, options); err != nil {
		return err
	}
	if s.crash {
		panic("crash after target owner commit")
	}
	return nil
}

type forkCrashRuntime struct {
	*fakeRuntime
	beforeCreate func(ports.RuntimeConfig)
}

func (r *forkCrashRuntime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	if handle.ID == "" {
		return false, errors.New("session id is required")
	}
	return r.fakeRuntime.IsAlive(ctx, handle)
}

func (r *forkCrashRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	if r.beforeCreate != nil {
		r.beforeCreate(cfg)
	}
	return r.fakeRuntime.Create(ctx, cfg)
}

func TestForkRecoverSwitchAfterTargetOwnerCommit(t *testing.T) {
	for _, afterLaunchIntent := range []bool{false, true} {
		t.Run(map[bool]string{false: "before runtime creation", true: "uncommitted launch"}[afterLaunchIntent], func(t *testing.T) {
			ctx := context.Background()
			st := sqlitetest.MustOpen(t)
			at := time.Now().UTC()
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at, Config: testRoleAgents()}); err != nil {
				t.Fatal(err)
			}
			rec, err := st.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
				CreatedAt: at, UpdatedAt: at, Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: at},
				Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir(), Branch: "branch", RuntimeHandleID: "source",
					RuntimeLaunchID: "source-launch", AgentSessionID: "source-native", Prompt: "ORIGINAL TASK"},
			})
			if err != nil {
				t.Fatal(err)
			}
			sw := domain.AgentSwitch{ID: "fork-switch-crash", SessionID: rec.ID, FromHarness: rec.Harness,
				TargetHarness: domain.HarnessCodex, IdempotencyKey: "crash-retry", RequestFingerprint: "v1:" + strings.Repeat("a", 64),
				SourceGenerationID: "source-launch", RequestedAt: at, UpdatedAt: at}
			if err := st.InsertForkAgentSwitch(ctx, sw); err != nil {
				t.Fatal(err)
			}
			runtime := &forkCrashRuntime{fakeRuntime: &fakeRuntime{}}
			agent := &recordingAgent{}
			deps := Deps{Runtime: runtime, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
				Store:     &forkOwnerCommitCrashStore{Store: st, crash: !afterLaunchIntent},
				Messenger: &fakeMessenger{}, Lifecycle: lifecycle.New(st, &fakeMessenger{}),
				LookPath: func(string) (string, error) { return "/bin/true", nil }}
			runtime.beforeCreate = func(cfg ports.RuntimeConfig) {
				saved, _, err := st.GetSession(ctx, rec.ID)
				if err != nil || saved.Metadata.RuntimeLaunchID == "" || saved.Metadata.RuntimeLaunchID != cfg.Env[EnvRuntimeLaunchID] {
					t.Fatalf("runtime started before durable launch intent: %+v, %v", saved.Metadata, err)
				}
				if afterLaunchIntent {
					if _, err := runtime.fakeRuntime.Create(ctx, cfg); err != nil {
						t.Fatal(err)
					}
					panic("crash with uncommitted runtime")
				}
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("switch did not reach crash boundary")
					}
				}()
				if err := New(deps).SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: sw.TargetHarness, HandoffNote: "HANDOFF NOTE"}); err != nil {
					t.Fatal(err)
				}
			}()
			committed, _, err := st.GetSession(ctx, rec.ID)
			if err != nil || committed.Metadata.RuntimeHandleID != "" || (committed.Metadata.RuntimeLaunchID != "") != afterLaunchIntent {
				t.Fatalf("unexpected crash boundary: %+v, %v", committed.Metadata, err)
			}
			stale := committed
			stale.Revision--
			wrongOwner := committed
			wrongOwner.Harness = rec.Harness
			for _, invalid := range []domain.SessionRecord{stale, wrongOwner} {
				if reserved, err := st.ReserveForkSwitchLaunch(ctx, invalid, "forged-launch"); err != nil || reserved {
					t.Fatalf("reserved a changed owner: %v, %v", reserved, err)
				}
			}
			created, destroyed := runtime.created, runtime.destroyed
			deps.Store, deps.Lifecycle = st, lifecycle.New(st, &fakeMessenger{})
			restarted := New(deps)
			svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st})
			svc.SetTerminalProviderSwitcher(restarted)
			if _, err := svc.ForkRecoverAgentSwitch(ctx, rec.ID, sw.ID); err != nil {
				t.Fatal(err)
			}
			if err := restarted.WaitAgentSwitchWorkers(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := st.ListForkAgentSwitches(ctx, rec.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("receipts: %+v, %v", rows, err)
			}
			if afterLaunchIntent {
				if rows[0].Terminal() || runtime.created != created || runtime.destroyed != destroyed {
					t.Fatalf("recovery touched uncommitted launch: receipt=%+v runtime=%+v", rows[0], runtime.fakeRuntime)
				}
				return
			}
			got, _, err := st.GetSession(ctx, rec.ID)
			if err != nil || rows[0].State != "completed" || got.Harness != sw.TargetHarness || runtime.created != 1 || agent.lastLaunch.Prompt != "HANDOFF NOTE" || got.Metadata.Prompt != "ORIGINAL TASK" {
				t.Fatalf("recovery did not settle committed target: %+v receipt=%+v launches=%d prompt=%q err=%v", got, rows[0], runtime.created, agent.lastLaunch.Prompt, err)
			}
		})
	}
}
