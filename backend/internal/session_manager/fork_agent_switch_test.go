package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
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

func TestForkFreshRecoveryRechecksPreLaunchState(t *testing.T) {
	for _, changed := range []string{"owner", "mode", "runtime handle", "launch intent", "native identity", "handoff"} {
		t.Run(changed, func(t *testing.T) {
			st := newFakeStore()
			rec := mkLive("mer-1")
			rec.Harness, rec.Mode, rec.Activity.State = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.ActivityExited
			rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionID = "", "", ""
			store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{HandoffNote: "handoff"}}
			switch changed {
			case "owner":
				rec.Harness = domain.HarnessCodex
			case "mode":
				rec.Mode = domain.SessionModeChat
			case "runtime handle":
				rec.Metadata.RuntimeHandleID = "new-runtime"
			case "launch intent":
				rec.Metadata.RuntimeLaunchID = "new-launch"
			case "native identity":
				rec.Metadata.AgentSessionID = "new-native"
			case "handoff":
				store.options.HandoffNote = ""
			}
			st.sessions[rec.ID] = rec
			runtime := &fakeRuntime{}
			m := New(Deps{Store: store, Runtime: runtime})
			if _, err := m.resumeAgentWithExpectedOwner(context.Background(), rec.ID, true, false, domain.HarnessClaudeCode); !errors.Is(err, ErrResumeInProgress) {
				t.Fatalf("changed pre-launch state was not fenced: %v", err)
			}
			if runtime.created != 0 || runtime.destroyed != 0 {
				t.Fatal("recovery touched a changed target")
			}
		})
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

type forkRecoveryClaude struct{ *claudecode.Plugin }

// Keep Claude's real restore/launch behavior without probing external credentials.
func (forkRecoveryClaude) ValidateLaunchAuth(context.Context, string, map[string]string) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}

func TestForkRecoverSwitchAfterTargetOwnerCommit(t *testing.T) {
	for _, tc := range []struct {
		name              string
		target            domain.AgentHarness
		afterLaunchIntent bool
	}{
		{"before runtime creation", domain.HarnessCodex, false},
		{"uncommitted launch", domain.HarnessCodex, true},
		{"return to Claude with an earlier transcript", domain.HarnessClaudeCode, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			afterLaunchIntent := tc.afterLaunchIntent
			ctx := context.Background()
			st := sqlitetest.MustOpen(t)
			at := time.Now().UTC()
			if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at, Config: testRoleAgents()}); err != nil {
				t.Fatal(err)
			}
			source := domain.HarnessClaudeCode
			if tc.target == domain.HarnessClaudeCode {
				source = domain.HarnessCodex
			}
			rec, err := st.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: source, Mode: domain.SessionModeTUI,
				CreatedAt: at, UpdatedAt: at, Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: at},
				Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir(), Branch: "branch", RuntimeHandleID: "source",
					RuntimeLaunchID: "source-launch", AgentSessionID: "source-native", Prompt: "ORIGINAL TASK"},
			})
			if err != nil {
				t.Fatal(err)
			}
			sw := domain.AgentSwitch{ID: "fork-switch-crash", SessionID: rec.ID, FromHarness: rec.Harness,
				TargetHarness: tc.target, IdempotencyKey: "crash-retry", RequestFingerprint: "v1:" + strings.Repeat("a", 64),
				SourceGenerationID: "source-launch", RequestedAt: at, UpdatedAt: at}
			if err := st.InsertForkAgentSwitch(ctx, sw); err != nil {
				t.Fatal(err)
			}
			agent := &recordingAgent{}
			var targetAgent ports.Agent = agent
			var earlierClaudeID string
			if tc.target == domain.HarnessClaudeCode {
				configDir, binDir := t.TempDir(), t.TempDir()
				t.Setenv("CLAUDE_CONFIG_DIR", configDir)
				t.Setenv("PATH", binDir)
				binary := "claude"
				if runtime.GOOS == "windows" {
					binary += ".exe"
				}
				if err := os.WriteFile(filepath.Join(binDir, binary), []byte("not executed by the fake runtime"), 0o755); err != nil {
					t.Fatal(err)
				}
				claude := forkRecoveryClaude{claudecode.New()}
				targetAgent = claude
				earlierClaudeID, _, err = claude.NativeConversationID(ctx, ports.SessionRef{ID: string(rec.ID)}, domain.SessionModeTUI, "")
				if err != nil || earlierClaudeID == "" {
					t.Fatalf("Claude fallback identity = %q, %v", earlierClaudeID, err)
				}
				transcripts := filepath.Join(configDir, "projects", "earlier-claude")
				if err := os.MkdirAll(transcripts, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(transcripts, earlierClaudeID+".jsonl"), []byte("{\"type\":\"user\",\"message\":\"before Codex\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runtime := &forkCrashRuntime{fakeRuntime: &fakeRuntime{}}
			deps := Deps{Runtime: runtime, Agents: singleAgent{agent: targetAgent}, Workspace: &fakeWorkspace{}, DataDir: t.TempDir(),
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
			if earlierClaudeID != "" {
				argv, _, mode, err := restoreArgv(ctx, targetAgent, rec.ID, rec.Metadata.WorkspacePath, committed.Metadata,
					"", "", ports.AgentConfig{}, rec.Kind, tc.target, deps.DataDir, nil)
				if err != nil || mode != RestoreModeNative || !slices.Contains(argv, earlierClaudeID) {
					t.Fatalf("fixture must reproduce Claude's old-transcript fallback: argv=%v mode=%s err=%v", argv, mode, err)
				}
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
			if err != nil || rows[0].State != "completed" || got.Harness != sw.TargetHarness || runtime.created != 1 || got.Metadata.Prompt != "ORIGINAL TASK" {
				t.Fatalf("recovery did not settle committed target: %+v receipt=%+v launches=%d prompt=%q err=%v", got, rows[0], runtime.created, agent.lastLaunch.Prompt, err)
			}
			if earlierClaudeID != "" {
				argv := runtime.lastCfg.Argv
				freshID := ""
				for i, arg := range argv {
					if arg == "--session-id" && i+1 < len(argv) {
						freshID = argv[i+1]
					}
				}
				if freshID == "" || freshID == earlierClaudeID || slices.Contains(argv, "--resume") || !slices.Contains(argv, "HANDOFF NOTE") || slices.Contains(argv, "ORIGINAL TASK") {
					t.Fatalf("recovery must start a new Claude conversation with the handoff, not %s: %v", earlierClaudeID, argv)
				}
			} else if agent.lastLaunch.Prompt != "HANDOFF NOTE" {
				t.Fatalf("recovered prompt = %q, want handoff", agent.lastLaunch.Prompt)
			}
		})
	}
}
