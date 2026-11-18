//go:build !linux

package verification

import (
	"os"
	"os/exec"
)

func preparePlatformCleanupConfirmation(*exec.Cmd) (*os.File, *os.File, int, error) {
	return nil, nil, -1, nil
}

func finalizePlatformCleanupConfirmation(_ *os.File, waitErr error) error { return waitErr }
