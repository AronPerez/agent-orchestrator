package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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
