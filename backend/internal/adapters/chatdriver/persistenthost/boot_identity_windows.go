//go:build windows

package persistenthost

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

type systemBootEnvironmentInformation struct {
	BootIdentifier windows.GUID
	FirmwareType   uint32
	_              uint32
	BootFlags      uint64
}

func currentBootIdentity() (string, error) {
	var info systemBootEnvironmentInformation
	var returned uint32
	if err := windows.NtQuerySystemInformation(
		windows.SystemBootEnvironmentInformation,
		unsafe.Pointer(&info),
		uint32(unsafe.Sizeof(info)),
		&returned,
	); err != nil {
		return "", err
	}
	if info.BootIdentifier == (windows.GUID{}) {
		return "", errors.New("system boot identifier is empty")
	}
	return "windows:" + info.BootIdentifier.String(), nil
}

// Windows starts each provider tree in a kill-on-close Job Object owned by the
// host, so a conclusively exited host also proves provider-tree absence.
func providerTreeDiesWithHost() bool { return true }
