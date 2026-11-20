package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// childProbeRuntime adds the child-process probe the production runtime has.
type childProbeRuntime struct {
	*fakeRuntime
	childAlive bool
}

func (r *childProbeRuntime) IsChildAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return r.childAlive, nil
}

func TestSwitchTargetThatExitsAtOnceIsReportedWithItsLastLine(t *testing.T) {
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })

	rec := domain.SessionRecord{ID: "s-1", Metadata: domain.SessionMetadata{RuntimeHandleID: "handle-1"}}
	dead := &childProbeRuntime{fakeRuntime: &fakeRuntime{outputs: []string{"starting\nError: Session ID 123 is already in use.\n\n"}}}
	err := (&Manager{runtime: dead}).confirmTUISwitchTargetAlive(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "Error: Session ID 123 is already in use.") {
		t.Fatalf("error = %v, want the agent's last line", err)
	}

	alive := &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}
	if err := (&Manager{runtime: alive}).confirmTUISwitchTargetAlive(context.Background(), rec); err != nil {
		t.Fatalf("a running target was reported as failed: %v", err)
	}
	// A runtime that cannot say whether the child is alive never fails a switch.
	if err := (&Manager{runtime: &fakeRuntime{}}).confirmTUISwitchTargetAlive(context.Background(), rec); err != nil {
		t.Fatalf("a runtime without the probe failed the switch: %v", err)
	}
}

// launchOptionsFakeStore keeps a session's launch options, as the real store does.
type launchOptionsFakeStore struct {
	*fakeStore
	options domain.AgentLaunchOptions
}

func (s *launchOptionsFakeStore) SessionLaunchOptions(context.Context, domain.SessionID) (domain.AgentLaunchOptions, error) {
	return s.options, nil
}

func (s *launchOptionsFakeStore) SetSessionLaunchOptions(_ context.Context, _ domain.SessionID, options domain.AgentLaunchOptions) error {
	s.options = options
	return nil
}

func (s *launchOptionsFakeStore) UpdateSessionWithLaunchOptions(ctx context.Context, rec domain.SessionRecord, options domain.AgentLaunchOptions) error {
	if err := s.UpdateSession(ctx, rec); err != nil {
		return err
	}
	s.options = options
	return nil
}

func TestRelaunchAfterACutShortSwitchHandsOverInsteadOfReplayingTheTask(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	// The row as a switch leaves it before its target starts: the target owns the
	// session with no native session yet, and its note is saved.
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "ORIGINAL TASK"})
	store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{HandoffNote: "HANDOFF NOTE"}}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: store,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})

	if _, err := m.RestoreWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastLaunch.Prompt != "HANDOFF NOTE" {
		t.Fatalf("relaunched prompt = %q, want the handoff note", agent.lastLaunch.Prompt)
	}
	if agent.lastLaunch.Config.Launch.HandoffNote != "" {
		t.Fatal("the note reached the agent's launch options")
	}
	if got := st.sessions["mer-1"].Metadata.Prompt; got != "ORIGINAL TASK" {
		t.Fatalf("session prompt = %q, want the task kept", got)
	}
	// No native session has been reported yet, so a later restore would start
	// fresh again: the note stays for it.
	if store.options.HandoffNote != "HANDOFF NOTE" {
		t.Fatalf("saved note = %q before a native session exists, want it kept", store.options.HandoffNote)
	}
	// Once the target has a native session, a restore resumes it and the note goes.
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionID = "target-native"
	st.sessions["mer-1"] = rec
	m.clearHandoffNote(context.Background(), "mer-1")
	if store.options.HandoffNote != "" {
		t.Fatalf("saved note = %q with a native session, want it cleared", store.options.HandoffNote)
	}
}

func TestSwitchTargetWithoutANativeSessionYetRestoresFromTheHandoff(t *testing.T) {
	ctx := context.Background()
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.KindWorker
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.Branch, rec.Metadata.Prompt = "branch", "ORIGINAL TASK ALREADY COMPLETED"
	rec.Metadata.AgentSessionID, rec.Metadata.RuntimeLaunchID = "source-native", "source-launch"
	st.sessions[rec.ID] = rec
	store := &launchOptionsFakeStore{fakeStore: st}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}, Agents: singleAgent{agent: agent},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	const handoff = "HANDOFF: original task done; the user wants a follow-up"
	if err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessMuse, HandoffNote: handoff}); err != nil {
		t.Fatal(err)
	}
	// The target stays alive but reports no native session before it exits.
	target := st.sessions[rec.ID]
	target.IsTerminated, target.Activity.State, target.Metadata.RuntimeHandleID = true, domain.ActivityExited, ""
	st.sessions[rec.ID] = target
	if _, err := m.RestoreWithMode(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if agent.lastLaunch.Prompt != handoff {
		t.Fatalf("restored prompt = %q, want the handoff instead of the original task", agent.lastLaunch.Prompt)
	}
}

func TestSwitchTUIProviderKeepsReservedReplacementFenced(t *testing.T) {
	for _, tc := range []struct {
		name      string
		launchID  string
		handleIDs []string
		probes    map[string]ports.FencedProbeResult
	}{
		{
			name: "live replacement", launchID: "replacement-generation", handleIDs: []string{"runtime-replacement"},
			probes: map[string]ports.FencedProbeResult{
				"runtime-replacement": {Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch},
			},
		},
		{
			name: "unknown replacement", launchID: "replacement-generation", handleIDs: []string{"runtime-replacement"},
			probes: map[string]ports.FencedProbeResult{
				"runtime-replacement": {Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed},
			},
		},
		{
			name: "matching generation unknown fallback", launchID: "published-generation",
			handleIDs: []string{"h1", "runtime-fallback"},
			probes: map[string]ports.FencedProbeResult{
				"runtime-fallback": {Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			rec := mkLive("mer-1")
			rec.Harness, rec.Mode, rec.Kind = domain.HarnessCodex, domain.SessionModeTUI, domain.KindWorker
			rec.Activity.State = domain.ActivityIdle
			rec.IssueID = "github:acme/mer#reserved"
			rec.Metadata.RuntimeLaunchID = "published-generation"
			st.sessions[rec.ID] = rec
			key := "mer\x00" + string(rec.IssueID)
			st.trackerIntakePending[key] = rec.ID
			st.trackerIntakeRecovery[rec.ID] = domain.TrackerIntakeRecoveryReservation{
				RuntimeLaunchID: tc.launchID, RuntimeHandleIDs: tc.handleIDs,
				DeliveryPossible: true,
			}
			runtime := &fakeRuntime{
				aliveByHandle: map[string]bool{rec.Metadata.RuntimeHandleID: true},
				fencedResults: tc.probes,
			}
			m := New(Deps{
				Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}}, Store: &launchOptionsFakeStore{fakeStore: st},
				Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
				LookPath: func(string) (string, error) { return "/bin/true", nil },
			})

			err := m.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{
				TargetHarness: domain.HarnessClaudeCode, HandoffNote: "handoff",
			})
			var switchErr *domain.ProviderSwitchError
			if !errors.As(err, &switchErr) || switchErr.Kind != domain.ProviderSwitchRefusalRecoveryRequired {
				t.Fatalf("SwitchTUIProvider() error = %v, want recovery required", err)
			}
			reservation := st.trackerIntakeRecovery[rec.ID]
			if reservation.CapacityReleased || reservation.RuntimeLaunchID != tc.launchID {
				t.Fatalf("tracker intake reservation = %+v, want replacement owner still fenced", reservation)
			}
			if runtime.created != 0 {
				t.Fatalf("started another runtime while replacement remained unresolved: %d", runtime.created)
			}
		})
	}
}

func TestSwitchTUIProviderRetainsPublishedRuntimeStop(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessCodex, domain.SessionModeTUI, domain.KindWorker
	rec.Activity.State = domain.ActivityIdle
	rec.IssueID = "github:acme/mer#stopped-published-runtime"
	rec.Metadata.AgentSessionID = "source-native"
	rec.Metadata.RuntimeLaunchID = "published-generation"
	st.sessions[rec.ID] = rec
	key := "mer\x00" + string(rec.IssueID)
	st.trackerIntakePending[key] = rec.ID
	st.trackerIntakeRecovery[rec.ID] = domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID: rec.Metadata.RuntimeLaunchID, RuntimeHandleIDs: []string{rec.Metadata.RuntimeHandleID},
		DeliveryPossible: true,
	}
	baseRuntime := &fakeRuntime{
		aliveByHandle: map[string]bool{rec.Metadata.RuntimeHandleID: true},
		fencedResults: map[string]ports.FencedProbeResult{
			rec.Metadata.RuntimeHandleID: {Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed},
		},
	}
	runtime := &childProbeRuntime{fakeRuntime: baseRuntime, childAlive: true}
	m := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}}, Store: &launchOptionsFakeStore{fakeStore: st},
		Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	if err := m.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, HandoffNote: "handoff",
	}); err != nil {
		t.Fatalf("SwitchTUIProvider(): %v", err)
	}
	if baseRuntime.created != 1 {
		t.Fatalf("target runtime creates = %d, want 1", baseRuntime.created)
	}
	if len(baseRuntime.fencedRefs) != 0 {
		t.Fatalf("already-stopped source was probed again: %+v", baseRuntime.fencedRefs)
	}
}

func TestSwitchTUIProviderDoesNotReleaseTrackerIntakeWhenDestroyFailsEvenIfExactWorkloadIsDead(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessCodex, domain.SessionModeTUI, domain.KindWorker
	rec.Activity.State = domain.ActivityIdle
	rec.IssueID = "github:acme/mer#uncertain-stop"
	rec.Metadata.AgentSessionID = "source-native"
	rec.Metadata.RuntimeLaunchID = "source-generation"
	st.sessions[rec.ID] = rec
	key := "mer\x00" + string(rec.IssueID)
	st.trackerIntakePending[key] = rec.ID
	st.trackerIntakeRecovery[rec.ID] = domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID: rec.Metadata.RuntimeLaunchID, RuntimeHandleIDs: []string{rec.Metadata.RuntimeHandleID},
		DeliveryPossible: true,
	}
	runtime := &fakeRuntime{
		destroyErr:    errors.New("pty-host still alive"),
		aliveByHandle: map[string]bool{rec.Metadata.RuntimeHandleID: true},
		fencedResults: map[string]ports.FencedProbeResult{
			rec.Metadata.RuntimeHandleID: {Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent},
		},
	}
	m := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}}, Store: &launchOptionsFakeStore{fakeStore: st},
		Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	err := m.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, HandoffNote: "handoff",
	})
	var switchErr *domain.ProviderSwitchError
	if !errors.As(err, &switchErr) || switchErr.Kind != domain.ProviderSwitchRefusalRecoveryRequired {
		t.Fatalf("SwitchTUIProvider() error = %v, want recovery required", err)
	}
	if reservation := st.trackerIntakeRecovery[rec.ID]; reservation.CapacityReleased {
		t.Fatalf("tracker intake reservation = %+v, want ownership fence retained", reservation)
	}
	if runtime.created != 0 {
		t.Fatalf("started target runtime while source ownership remained unresolved: %d", runtime.created)
	}
	if len(runtime.fencedRefs) != 0 {
		t.Fatalf("dead workload probes cannot prove terminal teardown: %+v", runtime.fencedRefs)
	}
}

func TestSwitchTargetThatMissesItsPromptLeavesTheSessionToTheRollback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override *string
		ended    int

		savedNote string
	}{
		{"ordinary relaunch", nil, 1, ""},
		// A restore that hands over a cut-short switch's saved note is not a switch:
		// nothing rolls it back.
		{"restore with a saved note", nil, 1, "SAVED NOTE"},
		{"provider switch", ptr("HANDOFF NOTE"), 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			rec := mkLive("mer-1")
			rec.Metadata.Prompt = "TASK"
			rec.Metadata.AgentSessionID = ""
			if tc.override != nil {
				rec.IssueID = "github:acme/mer#123"
				rec.Activity.State = domain.ActivityExited
				rec.Metadata.RuntimeLaunchID = "launch-old"
				rec.Metadata.RuntimeReleasedLaunchID = "launch-old"
				key := "mer\x00" + string(rec.IssueID)
				st.trackerIntakePending[key] = rec.ID
				st.trackerIntakeRecovery[rec.ID] = domain.TrackerIntakeRecoveryReservation{
					WorkspacePath:           rec.Metadata.WorkspacePath,
					RuntimeLaunchID:         rec.Metadata.RuntimeLaunchID,
					RuntimeHandleIDs:        []string{rec.Metadata.RuntimeHandleID},
					CapacityReleased:        true,
					ReleasedRuntimeLaunchID: rec.Metadata.RuntimeLaunchID,
				}
			}
			st.sessions[rec.ID] = rec
			store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{HandoffNote: tc.savedNote}}
			lcm := &fakeLCM{store: st}
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: afterStartAgent{recordingAgent: &recordingAgent{}}},
				Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{err: errors.New("delivery failed")},
				Lifecycle: lcm, LookPath: func(string) (string, error) { return "/bin/true", nil }})
			_, err := m.relaunchSessionWithPolicyAndGeneration(context.Background(), "relaunch", rec, st.projects["mer"],
				ports.WorkspaceInfo{Path: rec.Metadata.WorkspacePath}, nil, true, false, "", domain.SessionInterfaceTransitionHistoryStrict, tc.override)
			if err == nil || !strings.Contains(err.Error(), "deliver prompt") {
				t.Fatalf("error = %v, want the delivery failure", err)
			}
			if got := lcm.terminated[rec.ID]; got != tc.ended {
				t.Fatalf("session ended %d times, want %d", got, tc.ended)
			}
			if tc.override != nil {
				reservation := st.trackerIntakeRecovery[rec.ID]
				if reservation.DeliveryPossible || st.trackerIntakeHandled["mer\x00"+string(rec.IssueID)] != "" {
					t.Fatalf("switch handoff changed tracker delivery state: recovery=%+v handled=%q",
						reservation, st.trackerIntakeHandled["mer\x00"+string(rec.IssueID)])
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }

type scopedSwitchAuthAgent struct{ *launchAuthAgent }

func (scopedSwitchAuthAgent) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnauthorized, nil
}

func TestSwitchTUIProviderUsesProjectScopedAuthBeforeStoppingSource(t *testing.T) {
	for _, status := range []ports.AgentAuthStatus{ports.AgentAuthStatusAuthorized, ports.AgentAuthStatusUnauthorized} {
		t.Run(string(status), func(t *testing.T) {
			st := newFakeStore()
			config := testRoleAgents()
			config.Env = map[string]string{"ANTHROPIC_API_KEY": "project-test-key"}
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: config}
			rec := mkLive("mer-1")
			rec.Harness, rec.Mode, rec.Kind = domain.HarnessCodex, domain.SessionModeTUI, domain.KindWorker
			rec.Activity.State = domain.ActivityExited
			rec.Metadata.Branch, rec.Metadata.Prompt = "branch", "original task"
			st.sessions[rec.ID] = rec
			agent := scopedSwitchAuthAgent{&launchAuthAgent{recordingAgent: &recordingAgent{}, status: status}}
			runtime := &fakeRuntime{}
			m := New(Deps{Runtime: runtime, Agents: singleAgent{agent: agent}, Store: &launchOptionsFakeStore{fakeStore: st},
				Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
				LookPath: func(string) (string, error) { return "/bin/true", nil }})
			err := m.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{
				TargetHarness: domain.HarnessClaudeCode, HandoffNote: "handoff",
			})
			if agent.workingDir != rec.Metadata.WorkspacePath || agent.env["ANTHROPIC_API_KEY"] != "project-test-key" {
				t.Fatalf("auth ignored launch context: cwd=%q env=%v", agent.workingDir, agent.env)
			}
			if status == ports.AgentAuthStatusUnauthorized {
				if !errors.Is(err, ports.ErrAgentAuthRequired) || runtime.created != 0 || runtime.destroyed != 0 || st.sessions[rec.ID].Harness != rec.Harness {
					t.Fatalf("unauthorized preflight touched source: err=%v created=%d destroyed=%d", err, runtime.created, runtime.destroyed)
				}
			} else if err != nil || runtime.created != 1 || st.sessions[rec.ID].Harness != domain.HarnessClaudeCode {
				t.Fatalf("project-authorized target did not start: err=%v created=%d", err, runtime.created)
			}
		})
	}
}

func TestPreflightTUISwitchTargetWithholdsUnclaimedProviderKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	for _, tc := range []struct {
		name       string
		projectEnv map[string]string
		wantKey    string
	}{
		{name: "subscription login"},
		{name: "explicit project key", projectEnv: map[string]string{"ANTHROPIC_API_KEY": "project-key"}, wantKey: "project-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if err := providerkeys.Save(t.Context(), dataDir, "ANTHROPIC_API_KEY", "unclaimed-store-key"); err != nil {
				t.Fatal(err)
			}
			project := domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			project.Config.Env = tc.projectEnv
			rec := mkLive("mer-1")
			rec.Harness = domain.HarnessCodex
			for _, status := range []ports.AgentAuthStatus{ports.AgentAuthStatusAuthorized, ports.AgentAuthStatusUnauthorized} {
				target := scopedSwitchAuthAgent{&launchAuthAgent{recordingAgent: &recordingAgent{}, status: status}}
				m := New(Deps{DataDir: dataDir})
				err := m.preflightTUISwitchTarget(t.Context(), rec, project, domain.HarnessClaudeCode, target)
				if target.env["ANTHROPIC_API_KEY"] != tc.wantKey || target.workingDir != rec.Metadata.WorkspacePath {
					t.Fatalf("preflight used different credentials from launch: cwd=%q key=%q", target.workingDir, target.env["ANTHROPIC_API_KEY"])
				}
				if status == ports.AgentAuthStatusUnauthorized && !errors.Is(err, ports.ErrAgentAuthRequired) {
					t.Fatalf("unauthorized target was accepted: %v", err)
				}
				if status == ports.AgentAuthStatusAuthorized && err != nil {
					t.Fatalf("authorized target was refused: %v", err)
				}
			}
		})
	}
}

// runtimeAuthSwitchTarget is signed in only through a login AO saved itself,
// like Claude Code with a setup-token login and no native login.
type runtimeAuthSwitchTarget struct {
	fakeAgent
	dataDir string
}

func (runtimeAuthSwitchTarget) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnauthorized, nil
}

func (a runtimeAuthSwitchTarget) AuthStatusForRuntime(_ context.Context, dataDir string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	if dataDir != a.dataDir {
		return ports.AgentAuthStatusUnauthorized, domain.AgentAccountIdentity{}, nil, nil
	}
	return ports.AgentAuthStatusAuthorized, domain.AgentAccountIdentity{}, nil, nil
}

func TestPreflightTUISwitchTargetAcceptsAOSavedLogin(t *testing.T) {
	target := runtimeAuthSwitchTarget{dataDir: "/daemon/data"}
	m := New(Deps{DataDir: "/daemon/data"})
	if err := m.preflightTUISwitchTarget(context.Background(), domain.SessionRecord{}, domain.ProjectRecord{}, domain.HarnessClaudeCode, target); err != nil {
		t.Fatalf("preflight refused a target signed in through an AO-saved login: %v", err)
	}
	m = New(Deps{})
	if err := m.preflightTUISwitchTarget(context.Background(), domain.SessionRecord{}, domain.ProjectRecord{}, domain.HarnessClaudeCode, target); !errors.Is(err, ports.ErrAgentAuthRequired) {
		t.Fatalf("preflight without a data directory = %v, want the native sign-in refusal", err)
	}
}

func TestForcedTUISwitchInterruptsAWorkingAgent(t *testing.T) {
	ctx := context.Background()
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.KindWorker
	rec.Metadata.Branch, rec.Metadata.AgentSessionID = "branch", "source-native"
	st.sessions[rec.ID] = rec
	runtime := &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}
	runtime.onInterrupt = func(ports.RuntimeHandle) {
		current := st.sessions[rec.ID]
		current.Activity.State = domain.ActivityExited
		st.sessions[rec.ID] = current
	}
	m := New(Deps{Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: &launchOptionsFakeStore{fakeStore: st}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})

	var refusal *domain.ProviderSwitchError
	err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessMuse})
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalBusy {
		t.Fatalf("plain switch = %v, want a busy refusal", err)
	}
	err = m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode})
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalAlreadyActive || !errors.Is(err, domain.ErrProviderAlreadyActive) {
		t.Fatalf("switch to the running agent = %v, want already_active", err)
	}
	if err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessMuse, Force: true}); err != nil {
		t.Fatalf("forced switch: %v", err)
	}
	if len(runtime.interrupts) != 1 {
		t.Fatalf("interrupts = %v, want the working agent interrupted once", runtime.interrupts)
	}
	if got := st.sessions[rec.ID].Harness; got != domain.HarnessMuse {
		t.Fatalf("harness = %q, want the switch target", got)
	}
}

func TestTUISwitchToTheRunningAgentWithNewSettingsResumesItsOwnThread(t *testing.T) {
	ctx := context.Background()
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.KindWorker
	rec.Activity.State = domain.ActivityIdle
	rec.Metadata.Branch, rec.Metadata.Model, rec.Metadata.Effort, rec.Metadata.AgentSessionID = "branch", "sonnet", "low", "own-native"
	st.sessions[rec.ID] = rec
	store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{FastMode: true}}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}, Agents: singleAgent{agent: agent},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})

	same := domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, Model: "Sonnet", PermissionMode: domain.PermissionModeDefault}
	if err := m.SwitchTUIProvider(ctx, rec.ID, same); !errors.Is(err, domain.ErrProviderAlreadyActive) {
		t.Fatalf("unchanged settings = %v, want already_active", err)
	}
	if err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, Model: "opus", Effort: "high", PermissionMode: domain.PermissionModeAcceptEdits,
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions[rec.ID]
	if got.Harness != domain.HarnessClaudeCode || got.Metadata.AgentSessionID != rec.Metadata.AgentSessionID {
		t.Fatalf("harness = %q native = %q, want the same agent on its own thread", got.Harness, got.Metadata.AgentSessionID)
	}
	if got.Metadata.Permissions != domain.PermissionModeAcceptEdits || agent.lastRestore.Config.Permissions != domain.PermissionModeAcceptEdits {
		t.Fatalf("permission mode = %q, launched with %q", got.Metadata.Permissions, agent.lastRestore.Config.Permissions)
	}
	if agent.restoreCalls != 1 || agent.lastRestore.Config.Model != "opus" || agent.lastRestore.Config.Effort != "high" || agent.launchCalls != 0 {
		t.Fatalf("restores = %d launches = %d config = %+v, want one resume on the new settings", agent.restoreCalls, agent.launchCalls, agent.lastRestore.Config)
	}
	if !store.options.FastMode {
		t.Fatal("the restart dropped the session's launch options")
	}

	fresh := st.sessions[rec.ID]
	fresh.Metadata.AgentSessionID = ""
	st.sessions[rec.ID] = fresh
	var refusal *domain.ProviderSwitchError
	err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, Model: "haiku"})
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalUnsupported || agent.restoreCalls != 1 {
		t.Fatalf("restart with no thread to resume = %v, restores = %d; want a refusal before anything stops", err, agent.restoreCalls)
	}
}
