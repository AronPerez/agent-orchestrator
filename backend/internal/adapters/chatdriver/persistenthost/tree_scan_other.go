//go:build !darwin && !linux

package persistenthost

import "errors"

// providerTreeMember cannot list a provider tree on this platform, so recovery
// after an abrupt host exit stays fenced until reboot. Windows never reaches
// it: the host's kill-on-close Job Object already proves tree absence.
func providerTreeMember(int, string, string) (int, error) {
	return 0, errors.New("listing processes is not supported on this platform")
}
