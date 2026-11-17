//go:build linux

package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

const verificationOwnerEnvironment = "AO_VERIFICATION_OWNER"

func prepareUnixProcessOwnership(_ context.Context, cmd *exec.Cmd) (string, func() error, error) {
	owner := uuid.NewString()
	cmd.Env = newStepEnvironment(cmd.Env, map[string]string{verificationOwnerEnvironment: owner}).list
	return owner, func() error { return nil }, nil
}

func ownedVerificationProcesses(ctx context.Context, owner string) ([]unixOwnedProcess, error) {
	identities, err := procmem.Identities(ctx)
	if err != nil {
		return nil, err
	}
	marker := verificationOwnerEnvironment + "=" + owner
	owned := make([]unixOwnedProcess, 0)
	for pid, identity := range identities {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		environ, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read environment of process %d: %w", pid, err)
		}
		if !containsEnvironmentEntry(environ, marker) {
			continue
		}
		if !identity.Known() {
			return nil, fmt.Errorf("identity of verification process %d is unknown", pid)
		}
		owned = append(owned, unixOwnedProcess{pid: pid, start: identity.Start})
	}
	return owned, nil
}

func signalOwnedVerificationProcess(process unixOwnedProcess, signal syscall.Signal) error {
	return signalUnixProcessIfCurrent(process, signal, linuxProcessStart, syscall.Kill)
}

func linuxProcessStart(pid int) (string, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return "", errUnixProcessGone
	}
	if err != nil {
		return "", err
	}
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", errors.New("malformed process stat")
	}
	fields := strings.Fields(string(stat[end+1:]))
	const startField = 22 - 3
	if len(fields) <= startField {
		return "", errors.New("truncated process stat")
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return "", errUnixProcessGone
	}
	if _, err := strconv.ParseUint(fields[startField], 10, 64); err != nil {
		return "", errors.New("invalid process start time")
	}
	return fields[startField], nil
}

func containsEnvironmentEntry(data []byte, entry string) bool {
	for len(data) > 0 {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			return string(data) == entry
		}
		if string(data[:end]) == entry {
			return true
		}
		data = data[end+1:]
	}
	return false
}
