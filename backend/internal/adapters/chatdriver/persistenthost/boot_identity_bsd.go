//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package persistenthost

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

func currentBootIdentity() (string, error) {
	bootTime, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%d", runtime.GOOS, bootTime.Sec, bootTime.Usec), nil
}

func providerTreeDiesWithHost() bool { return false }
