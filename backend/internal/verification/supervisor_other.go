//go:build !linux

package verification

// RunLinuxSupervisorIfRequested is inert outside Linux.
func RunLinuxSupervisorIfRequested([]string) (bool, int, error) {
	return false, 0, nil
}
