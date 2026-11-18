//go:build linux

package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

func prepareUnixProcessOwnership(_ context.Context, cmd *exec.Cmd) (unixProcessOwnership, func() error, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return unixProcessOwnership{}, nil, fmt.Errorf("create verification ownership pipe: %w", err)
	}
	closeOwnership := func() error { return errors.Join(writer.Close(), reader.Close()) }
	descriptor, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(writer.Fd())))
	if err != nil {
		return unixProcessOwnership{}, nil, errors.Join(
			fmt.Errorf("identify verification ownership pipe: %w", err), reader.Close(), writer.Close(),
		)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	token := uuid.NewString()
	cmd.Env = newStepEnvironment(cmd.Env, map[string]string{verificationOwnerEnvironment: token}).list
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, writer)
	return unixProcessOwnership{
		token: token, descriptor: descriptor, fd: fd, daemonPID: os.Getpid(),
	}, closeOwnership, nil
}

func ownedVerificationProcesses(
	ctx context.Context,
	owner unixProcessOwnership,
	descendants *unixDescendantWatch,
) ([]unixOwnedProcess, error) {
	identities, tracked, err := sharedUnixDescendantObserver.current(ctx, descendants)
	if err != nil {
		return nil, err
	}
	marker := verificationOwnerEnvironment + "=" + owner.token
	owned := make(map[unixOwnedProcess]struct{}, len(tracked))
	for _, process := range tracked {
		if process.pid != owner.daemonPID {
			owned[process] = struct{}{}
		}
	}
	for pid, identity := range identities {
		if pid == owner.daemonPID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		marked := false
		environ, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		switch {
		case err == nil:
			marked = containsEnvironmentEntry(environ, marker)
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		default:
			return nil, fmt.Errorf("read environment of process %d: %w", pid, err)
		}
		if !marked {
			descriptor, descriptorErr := os.Readlink(
				"/proc/" + strconv.Itoa(pid) + "/fd/" + strconv.Itoa(owner.fd),
			)
			switch {
			case descriptorErr == nil:
				marked = descriptor == owner.descriptor
			case errors.Is(descriptorErr, fs.ErrNotExist), errors.Is(descriptorErr, fs.ErrPermission):
			default:
				return nil, fmt.Errorf("read descriptor of process %d: %w", pid, descriptorErr)
			}
		}
		if !marked {
			continue
		}
		if !identity.Known() {
			return nil, fmt.Errorf("identity of verification process %d is unknown", pid)
		}
		process, current, startErr := unixProcessFromSnapshot(pid, identity.Start, currentVerificationProcessStart)
		if startErr != nil {
			return nil, fmt.Errorf("recheck verification process %d: %w", pid, startErr)
		}
		if !current {
			continue
		}
		owned[process] = struct{}{}
	}
	result := make([]unixOwnedProcess, 0, len(owned))
	for process := range owned {
		result = append(result, process)
	}
	return result, nil
}

func signalOwnedVerificationProcess(process unixOwnedProcess, signal syscall.Signal) error {
	return signalUnixProcessIfCurrent(process, signal, currentVerificationProcessStart, syscall.Kill)
}

func currentVerificationProcessStart(pid int) (string, error) {
	return linuxVerificationProcessStart(pid, false)
}

func verificationProcessStartForWatch(pid int) (string, error) {
	return linuxVerificationProcessStart(pid, true)
}

func linuxVerificationProcessStart(pid int, includeZombie bool) (string, error) {
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
	if !includeZombie && (fields[0] == "Z" || fields[0] == "X") {
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
