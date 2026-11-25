package acp

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// planModeConfig is a binding whose ACP modes are approval tiers plus a native
// plan mode, which only some of its servers offer (agy acp's "plan").
func planModeConfig(planMode string) Config {
	return Config{
		Harness:      domain.HarnessAgy,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMode: func(permission ports.PermissionMode) string {
			if ports.NormalizePermissionMode(permission) == ports.PermissionModeAcceptEdits {
				return "accept-edits"
			}
			return "default"
		},
		PlanModeForAgent: func(acpsdk.InitializeResponse) string { return planMode },
	}
}

func sendTurnWithSettings(t *testing.T, conv ports.ChatConversation, settings ports.ChatTurnSettings) {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "do the task", Settings: settings})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	completed := false
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = true
		}
		if completed && event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			return
		}
	}
}

func startPlanModeConversation(t *testing.T, planMode string) (*fakeAgent, ports.ChatConversation) {
	t.Helper()
	agent := &fakeAgent{promptNoPermission: true}
	driver := New(planModeConfig(planMode), slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	_ = nextEvent(t, conv.Events()) // controller.ready
	return agent, conv
}

func agentMode(agent *fakeAgent) string {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.mode
}

// A turn AO asks to plan runs in the agent's own plan mode, and the next turn
// goes back to the mode its approval policy selects.
func TestACPDriverRunsAPlanTurnInTheAgentsPlanMode(t *testing.T) {
	agent, conv := startPlanModeConversation(t, "plan")
	if !conv.Capabilities().Has(ports.ChatCapabilityPlanMode) {
		t.Fatal("a binding with a plan mode for this agent must advertise plan_mode")
	}

	sendTurnWithSettings(t, conv, ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits, Plan: true})
	if got := agentMode(agent); got != "plan" {
		t.Fatalf("plan turn ran in mode %q, want plan", got)
	}

	sendTurnWithSettings(t, conv, ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits})
	if got := agentMode(agent); got != "accept-edits" {
		t.Fatalf("turn after plan ran in mode %q, want the approval mode accept-edits", got)
	}
}

// An agent with no plan mode (the separate agy_acp_server) does not advertise
// plan_mode, and a stray plan request keeps the approval mode.
func TestACPDriverWithoutAPlanModeIgnoresPlanTurns(t *testing.T) {
	agent, conv := startPlanModeConversation(t, "")
	if conv.Capabilities().Has(ports.ChatCapabilityPlanMode) {
		t.Fatal("plan_mode advertised without a plan mode")
	}

	sendTurnWithSettings(t, conv, ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits, Plan: true})
	if got := agentMode(agent); got != "accept-edits" {
		t.Fatalf("plan request ran in mode %q, want the approval mode accept-edits", got)
	}
}

// While a turn plans, every permission request goes to the user, whatever the
// approval mode: an auto policy would otherwise approve the agent's own
// plan-review or leave-plan gate and let it start editing unseen. The next
// turn that does not plan applies the policy again.
func TestACPDriverAsksTheUserForEveryPermissionWhilePlanning(t *testing.T) {
	cfg := planModeConfig("plan")
	cfg.Capabilities[ports.ChatCapabilityApprovals] = true
	cfg.PermissionPolicy = func(mode ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
		if ports.NormalizePermissionMode(mode) != ports.PermissionModeAuto {
			return "", false
		}
		return params.Options[0].OptionId, true
	}
	agent := &fakeAgent{promptNoPermission: true}
	driver := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	_ = nextEvent(t, opened.Events()) // controller.ready
	conv := opened.(*conversation)
	request := acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{ToolCallId: "exit-plan-1", Title: acpsdk.Ptr("Leave plan mode and start implementing")},
		Options:  []acpsdk.PermissionOption{{OptionId: "allow-once", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce}},
	}

	if err := conv.applyTurnSettings(context.Background(), ports.ChatTurnSettings{Approval: ports.PermissionModeAuto, Plan: true}); err != nil {
		t.Fatalf("plan turn settings: %v", err)
	}
	answered := make(chan acpsdk.RequestPermissionResponse, 1)
	go func() {
		response, _ := conv.RequestPermission(context.Background(), request)
		answered <- response
	}()
	select {
	case response := <-answered:
		t.Fatalf("a permission request during a plan turn was answered automatically: %#v", response)
	case event := <-opened.Events():
		if event.Kind != ports.ChatEventApprovalRequested {
			t.Fatalf("event = %q, want the request parked for the user", event.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the permission request was neither answered nor parked")
	}

	if err := conv.applyTurnSettings(context.Background(), ports.ChatTurnSettings{Approval: ports.PermissionModeAuto}); err != nil {
		t.Fatalf("normal turn settings: %v", err)
	}
	request.ToolCall.ToolCallId = "edit-1"
	response, err := conv.RequestPermission(context.Background(), request)
	if err != nil || response.Outcome.Selected == nil || response.Outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("after planning the auto policy must apply again; response %#v, err %v", response, err)
	}
}
