package sessionmanager

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestResolveSessionModeKeepsProjectPinForOrchestrators(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		requested, project, want domain.SessionMode
	}{
		{"project overrides orchestrator default", "", domain.SessionModeTUI, domain.SessionModeTUI},
		{"explicit mode overrides project", domain.SessionModeChat, domain.SessionModeTUI, domain.SessionModeChat},
		{"unknown project mode keeps orchestrator default", "", "voice", domain.SessionModeChat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{defaults: fixedSessionModeDefaults(domain.SessionModeTUI)}
			got := m.resolveSessionMode(t.Context(), domain.KindOrchestrator, tc.requested, domain.ProjectConfig{SessionInterface: tc.project})
			if got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

// The cloud control plane resolves a cloud session's interface with the same
// precedence, against the same cases (cloud/internal/domain), so a change to
// either side fails the other's test until both agree.
func TestStartSessionModeMatchesTheSharedCases(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/session-start-interface.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Kind      string `json:"kind"`
			Requested string `json:"requested"`
			Default   string `json:"default"`
			Want      string `json:"want"`
		} `json:"cases"`
		WithoutDefault struct {
			Local []struct {
				Kind      string `json:"kind"`
				Requested string `json:"requested"`
				Want      string `json:"want"`
			} `json:"local"`
		} `json:"withoutDefault"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range fixture.Cases {
		got := startSessionMode(domain.SessionKind(tc.Kind), domain.SessionMode(tc.Requested), domain.SessionMode(tc.Default))
		if string(got) != tc.Want {
			t.Errorf("startSessionMode(%s, requested %q, default %q) = %q, want %q", tc.Kind, tc.Requested, tc.Default, got, tc.Want)
		}
	}
	// An unset default is the empty string here; orchestrators still start in
	// Chat (the cloud's rule for a create without one differs).
	if len(fixture.WithoutDefault.Local) == 0 {
		t.Fatal("no local cases without a default")
	}
	for _, tc := range fixture.WithoutDefault.Local {
		got := startSessionMode(domain.SessionKind(tc.Kind), domain.SessionMode(tc.Requested), "")
		if string(got) != tc.Want {
			t.Errorf("startSessionMode(%s, requested %q, no default) = %q, want %q", tc.Kind, tc.Requested, got, tc.Want)
		}
	}
}
