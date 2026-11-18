//go:build !linux && !windows

package verification

import (
	"fmt"
	"os/exec"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func configureUnixWorkspaceProcess(cmd *exec.Cmd) {
	aoprocess.ConfigureTreeCancellation(cmd)
}

func configureUnixProcessStartGate(cmd *exec.Cmd, fd int) error {
	original := append([]string{cmd.Path}, cmd.Args[1:]...)
	if len(cmd.Args) == 3 && cmd.Args[1] == "-c" {
		// A shell invoked as `sh -c script` exposes its argv[0] as $0. Passing
		// the original name after the script preserves that contract when the
		// gate re-execs the resolved shell path.
		original = append(original, cmd.Args[0])
	}
	gateScript := fmt.Sprintf(
		"if ! IFS= read -r _ <&%d; then exit 125; fi\nexec %d<&-\nexec \"$@\"",
		fd,
		fd,
	)
	cmd.Args = append([]string{cmd.Args[0], "-c", gateScript, "ao-verification-gate"}, original...)
	return nil
}
