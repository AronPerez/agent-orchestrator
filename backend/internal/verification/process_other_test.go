//go:build !windows

package verification

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerHostShellUsesRequestedWorkspaceAndEnvironment(t *testing.T) {
	workspace := t.TempDir()
	result, err := NewRunner().Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: workspace,
		Commands: []string{`printf '%s|%s' "$PWD" "$VERIFY_VALUE"`},
		Env:      map[string]string{"VERIFY_VALUE": "from-project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !strings.Contains(result.OutputTail, workspace+"|[REDACTED]") {
		t.Fatalf("result = %+v", result)
	}
}
