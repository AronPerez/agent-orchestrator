//go:build !windows && !linux && !darwin

package verification

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

func prepareUnixProcessOwnership(context.Context, *exec.Cmd) (string, func() error, error) {
	return "", func() error { return nil }, errors.New("verification process ownership is unsupported on this platform")
}

func ownedVerificationProcesses(context.Context, string) ([]unixOwnedProcess, error) {
	return nil, errors.New("verification process ownership is unsupported on this platform")
}

func signalOwnedVerificationProcess(unixOwnedProcess, syscall.Signal) error {
	return errors.New("verification process ownership is unsupported on this platform")
}
