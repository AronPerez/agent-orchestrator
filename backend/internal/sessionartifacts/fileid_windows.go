//go:build windows

package sessionartifacts

import (
	"io/fs"

	"golang.org/x/sys/windows"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Windows' FileInfo omits the volume and file index needed for rename
// tracking. fullIdentity opens the path without following reparse points and
// reads the stable identity from its handle instead.
func quickIdentity(fs.FileInfo) domain.FileIdentity { return domain.FileIdentity{} }

func fullIdentity(path string, _ fs.FileInfo) domain.FileIdentity {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return domain.FileIdentity{}
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return domain.FileIdentity{}
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil {
		return domain.FileIdentity{}
	}
	return domain.FileIdentity{
		Dev:   uint64(info.VolumeSerialNumber),
		Ino:   uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
		Birth: info.CreationTime.Nanoseconds(),
	}
}
