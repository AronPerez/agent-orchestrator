//go:build !linux

package verification

func platformCleanupUncertain(error) bool { return false }
