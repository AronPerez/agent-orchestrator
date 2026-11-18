//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package persistenthost

import "errors"

func currentBootIdentity() (string, error) {
	return "", errors.New("system boot identity is unavailable on this platform")
}

func providerTreeDiesWithHost() bool { return false }
