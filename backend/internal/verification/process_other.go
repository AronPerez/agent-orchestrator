//go:build !windows

package verification

import (
	"context"
	"os/exec"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// unixWorkspaceWrapper holds the command shell alive after its command list
// finishes. While it waits for the parent acknowledgment, its pid still owns
// the process group and every child, including one that called setsid, still
// has a captured ancestry. The EXIT trap also covers `exit`, set -e, and an
// ordinary command failure while preserving sh -c's final status.
const unixWorkspaceWrapper = `
ao_verification_finish() {
	ao_verification_status=$?
	trap - 0
	printf '.\n' >&4
	IFS= read -r ao_verification_release <&5 || :
	exit "$ao_verification_status"
}
trap ao_verification_finish 0
IFS= read -r ao_verification_start <&3 || exit 125
ao_verification_command=$1
shift
eval "$ao_verification_command"
`

func workspaceStepCommand(ctx context.Context, command string) *exec.Cmd {
	// Use "sh" as argv[0] and shift the command away before eval so project
	// commands observe the same positional parameters as `sh -c command`.
	return aoprocess.CommandContext(ctx, "sh", "-c", unixWorkspaceWrapper, "sh", command)
}
