//go:build darwin

package persistenthost

import (
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

func currentBootIdentity() (string, error) {
	return darwinBootIdentity(unix.Sysctl)
}

func darwinBootIdentity(readSysctl func(string) (string, error)) (string, error) {
	id, err := readSysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "", errors.New("boot session UUID is empty")
	}
	return "darwin:" + id, nil
}

func providerTreeDiesWithHost() bool { return false }
