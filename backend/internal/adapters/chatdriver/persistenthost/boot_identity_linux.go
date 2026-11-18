//go:build linux

package persistenthost

import (
	"errors"
	"os"
	"strings"
)

func currentBootIdentity() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id") //nolint:gosec // Fixed kernel-provided boot identity path.
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", errors.New("kernel boot identity is empty")
	}
	return "linux:" + id, nil
}

func providerTreeDiesWithHost() bool { return false }
