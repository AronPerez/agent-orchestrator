package acp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// advertisesAppend is the test binding's handshake check: a provider that
// advertises a system-prompt extension takes the standing context there.
func advertisesAppend(init acpsdk.InitializeResponse) bool {
	return init.AgentCapabilities.Meta["systemPromptAppend"] == true
}

// handshakeStandingConfig keeps the preamble binding's defaults and adds
// handshake-selected replacements, like a harness whose newer server offers a
// system-prompt extension and whose older one does not.
func handshakeStandingConfig() Config {
	cfg := preambleConfig()
	cfg.SessionMeta = func(LaunchConfig) map[string]any { return map[string]any{"default": true} }
	cfg.SessionMetaForAgent = func(init acpsdk.InitializeResponse) func(LaunchConfig) map[string]any {
		if !advertisesAppend(init) {
			return nil
		}
		return func(launch LaunchConfig) map[string]any {
			return map[string]any{"append": launch.SystemPrompt}
		}
	}
	cfg.SessionPreambleForAgent = func(init acpsdk.InitializeResponse) func(LaunchConfig) []acpsdk.ContentBlock {
		if !advertisesAppend(init) {
			return nil
		}
		return func(LaunchConfig) []acpsdk.ContentBlock { return nil }
	}
	return cfg
}

func TestACPDriverSelectsStandingContextChannelFromTheHandshake(t *testing.T) {
	for _, tc := range []struct {
		name         string
		advertised   bool
		wantMeta     map[string]any
		wantPreamble string
	}{
		{name: "advertised", advertised: true, wantMeta: map[string]any{"append": standingInstructions}},
		{name: "not advertised", wantMeta: map[string]any{"default": true}, wantPreamble: standingInstructions},
	} {
		newAgent := func() *fakeAgent {
			caps := acpsdk.AgentCapabilities{
				LoadSession:         true,
				SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
			}
			if tc.advertised {
				caps.Meta = map[string]any{"systemPromptAppend": true}
			}
			return &fakeAgent{promptNoPermission: true, capabilities: &caps}
		}
		newDriver := func(agent *fakeAgent) *Driver {
			driver := New(handshakeStandingConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			driver.useTestProcess(fakeSpawn(agent))
			return driver
		}

		t.Run(tc.name+"/start", func(t *testing.T) {
			agent := newAgent()
			conv, err := newDriver(agent).Start(context.Background(), ports.ChatStartConfig{
				WorkspacePath: t.TempDir(), SystemPrompt: standingInstructions,
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer conv.Close()
			_ = nextEvent(t, conv.Events()) // controller.ready
			prompt := runOneTurn(t, agent, conv, "do the task")
			agent.mu.Lock()
			meta := agent.newParams.Meta
			agent.mu.Unlock()
			assertStandingChannel(t, meta, prompt, tc.wantMeta, tc.wantPreamble)
		})

		t.Run(tc.name+"/resume", func(t *testing.T) {
			agent := newAgent()
			conv, err := newDriver(agent).Resume(context.Background(), ports.ChatResumeConfig{
				SessionID: "worker-1", ProviderConversationID: "provider-session-1",
				WorkspacePath: t.TempDir(), SystemPrompt: standingInstructions,
			})
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			defer conv.Close()
			_ = nextEvent(t, conv.Events()) // controller.ready
			prompt := runOneTurn(t, agent, conv, "do the task")
			agent.mu.Lock()
			meta := agent.loadParams.Meta
			agent.mu.Unlock()
			assertStandingChannel(t, meta, prompt, tc.wantMeta, tc.wantPreamble)
		})
	}
}

func assertStandingChannel(t *testing.T, meta map[string]any, prompt []acpsdk.ContentBlock, wantMeta map[string]any, wantPreamble string) {
	t.Helper()
	if len(meta) != len(wantMeta) {
		t.Fatalf("session _meta = %#v, want %#v", meta, wantMeta)
	}
	for key, want := range wantMeta {
		if meta[key] != want {
			t.Fatalf("session _meta = %#v, want %#v", meta, wantMeta)
		}
	}
	if got := preambleText(prompt); got != wantPreamble {
		t.Fatalf("first prompt standing context = %q, want %q (prompt %#v)", got, wantPreamble, prompt)
	}
}
