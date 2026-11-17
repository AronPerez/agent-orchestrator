//go:build darwin

package verification

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	darwinZombieState       = 5
	darwinMarkerFDMinimum   = 64
	darwinMarkerFDMaximum   = 255
	darwinOwnershipDivider  = "\n"
	darwinLsofPath          = "/usr/sbin/lsof"
	darwinWrapperProcessArg = "ao-verification-wrapper"
)

func prepareUnixProcessOwnership(ctx context.Context, cmd *exec.Cmd) (string, func() error, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", nil, fmt.Errorf("create verification ownership pipe: %w", err)
	}
	closeOwnership := func() error { return errors.Join(reader.Close(), writer.Close()) }
	fail := func(cause error) (string, func() error, error) {
		return "", nil, errors.Join(cause, closeOwnership())
	}

	markerName, err := darwinDescriptorName(ctx, os.Getpid(), int(writer.Fd()))
	if err != nil {
		return fail(fmt.Errorf("identify verification ownership pipe: %w", err))
	}
	sourceFD := 3 + len(cmd.ExtraFiles)
	targetFD, err := randomDarwinMarkerFD(sourceFD)
	if err != nil {
		return fail(fmt.Errorf("select verification ownership descriptor: %w", err))
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, writer)

	if len(cmd.Args) != 3 || cmd.Args[1] != "-c" {
		return fail(errors.New("verification shell command has an unexpected argument layout"))
	}
	originalArg0, command := cmd.Args[0], cmd.Args[2]
	wrapper := fmt.Sprintf(
		`exec %d>&%d; exec %d>&-; exec -a "$1" "$2" -c "$3"`,
		targetFD, sourceFD, sourceFD,
	)
	cmd.Args = []string{
		originalArg0, "-c", wrapper, darwinWrapperProcessArg,
		originalArg0, cmd.Path, command,
	}
	owner := strconv.Itoa(targetFD) + darwinOwnershipDivider + markerName
	return owner, closeOwnership, nil
}

func randomDarwinMarkerFD(sourceFD int) (int, error) {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		return 0, err
	}
	if limit.Cur == 0 {
		return 0, errors.New("open-file limit is zero")
	}
	minimum := max(darwinMarkerFDMinimum, sourceFD+1)
	maximum := min(uint64(darwinMarkerFDMaximum), limit.Cur-1)
	if uint64(minimum) > maximum {
		return 0, fmt.Errorf("no descriptor available between %d and %d", minimum, maximum)
	}
	span := big.NewInt(int64(maximum) - int64(minimum) + 1)
	offset, err := rand.Int(rand.Reader, span)
	if err != nil {
		return 0, err
	}
	return minimum + int(offset.Int64()), nil
}

func darwinDescriptorName(ctx context.Context, pid, fd int) (string, error) {
	cmd := aoprocess.CommandContext(
		ctx, darwinLsofPath, "-nP", "-a", "-p", strconv.Itoa(pid), "-d", strconv.Itoa(fd), "-F", "n",
	)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return line[1:], nil
		}
	}
	return "", errors.New("ownership pipe was absent from lsof output")
}

func ownedVerificationProcesses(ctx context.Context, owner string) ([]unixOwnedProcess, error) {
	fdText, markerName, ok := strings.Cut(owner, darwinOwnershipDivider)
	if !ok {
		return nil, errors.New("invalid Darwin verification owner")
	}
	fd, err := strconv.Atoi(fdText)
	if err != nil || fd < 0 {
		return nil, errors.New("invalid Darwin verification owner descriptor")
	}

	cmd := aoprocess.CommandContext(ctx, darwinLsofPath, "-nP", "-a", "-d", strconv.Itoa(fd), "-F", "pn")
	output, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	owned := make([]unixOwnedProcess, 0)
	pid := 0
	for _, line := range strings.Split(string(output), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case pid > 0 && strings.HasPrefix(line, "n") && line[1:] == markerName:
			start, startErr := darwinProcessStart(pid)
			if errors.Is(startErr, errUnixProcessGone) {
				continue
			}
			if startErr != nil {
				return nil, fmt.Errorf("identify verification process %d: %w", pid, startErr)
			}
			owned = append(owned, unixOwnedProcess{pid: pid, start: start})
		}
	}
	return owned, nil
}

func signalOwnedVerificationProcess(process unixOwnedProcess, signal syscall.Signal) error {
	return signalUnixProcessIfCurrent(process, signal, darwinProcessStart, syscall.Kill)
}

func darwinProcessStart(pid int) (string, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) {
		return "", errUnixProcessGone
	}
	if err != nil {
		return "", err
	}
	if process.Proc.P_stat == darwinZombieState {
		return "", errUnixProcessGone
	}
	return darwinProcessIdentity(*process), nil
}

func darwinProcessIdentity(process unix.KinfoProc) string {
	started := process.Proc.P_starttime
	return strconv.FormatInt(started.Sec, 10) + "." + strconv.FormatInt(int64(started.Usec), 10)
}
