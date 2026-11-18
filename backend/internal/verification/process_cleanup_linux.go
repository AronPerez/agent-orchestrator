//go:build linux

package verification

import (
	"errors"
	"os/exec"
)

func platformCleanupUncertain(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == linuxSupervisorUncertainCode
}
