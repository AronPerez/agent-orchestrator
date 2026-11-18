//go:build !windows && !linux && !darwin

package verification

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

func prepareUnixProcessOwnership(context.Context, *exec.Cmd) (unixProcessOwnership, func() error, error) {
	return unixProcessOwnership{}, func() error { return nil }, errors.New("verification process ownership is unsupported on this platform")
}

func ownedVerificationProcesses(context.Context, unixProcessOwnership, *unixDescendantWatch) ([]unixOwnedProcess, error) {
	return nil, errors.New("verification process ownership is unsupported on this platform")
}

func currentVerificationProcessStart(int) (string, error) {
	return "", errors.New("verification process ownership is unsupported on this platform")
}

func verificationProcessStartForWatch(int) (string, error) {
	return "", errors.New("verification process ownership is unsupported on this platform")
}

func signalOwnedVerificationProcess(unixOwnedProcess, syscall.Signal) error {
	return errors.New("verification process ownership is unsupported on this platform")
}
