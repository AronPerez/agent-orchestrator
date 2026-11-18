//go:build linux

package verification

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func preparePlatformCleanupConfirmation(cmd *exec.Cmd) (*os.File, *os.File, int, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("create verification cleanup confirmation: %w", err)
	}
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, writer)
	return reader, writer, fd, nil
}

func finalizePlatformCleanupConfirmation(reader *os.File, waitErr error) error {
	if reader == nil {
		return errors.Join(waitErr, markCleanupUncertain(errors.New("verification cleanup confirmation is unavailable")))
	}
	var confirmation [1]byte
	_, err := io.ReadFull(reader, confirmation[:])
	if err == nil && confirmation[0] == linuxSupervisorCleanupComplete {
		return waitErr
	}
	if err == nil {
		err = fmt.Errorf("invalid verification cleanup confirmation %q", confirmation[0])
	} else {
		err = fmt.Errorf("read verification cleanup confirmation: %w", err)
	}
	return errors.Join(waitErr, markCleanupUncertain(err))
}
