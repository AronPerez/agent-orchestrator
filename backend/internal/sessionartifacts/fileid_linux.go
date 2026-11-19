//go:build linux

package sessionartifacts

import (
	"io/fs"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fsIOCGetVersion is FS_IOC_GETVERSION, _IOR('v', 1, long): the inode's
// generation number, which ext4, btrfs and XFS change whenever they reuse an
// inode number for a new file.
const fsIOCGetVersion = 2<<30 | uint(unsafe.Sizeof(int(0)))<<16 | 'v'<<8 | 1

var nameToHandleAt = unix.NameToHandleAt

// quickIdentity is the device, inode and modification time in info. The
// birth time and generation take a statx and an open, which fullIdentity adds.
func quickIdentity(info fs.FileInfo) domain.FileIdentity {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return domain.FileIdentity{}
	}
	return domain.FileIdentity{Dev: uint64(st.Dev), Ino: st.Ino, ModTime: info.ModTime().UnixNano()} //nolint:unconvert // Dev is not uint64 on every Linux architecture
}

// fullIdentity adds the birth time and a stable object generation where the
// file system records them. Linux birth times are only as fine as the kernel's
// coarse clock, so a file written right after another was deleted can share its
// inode and birth time. Prefer FS_IOC_GETVERSION, then a kernel file handle,
// both of which distinguish inode reuse and survive a rename plus edit.
func fullIdentity(path string, info fs.FileInfo) domain.FileIdentity {
	id := quickIdentity(info)
	if id.Ino == 0 {
		return id
	}
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME|unix.STATX_INO, &stx); err == nil &&
		stx.Mask&unix.STATX_BTIME != 0 && stx.Ino == id.Ino {
		id.Birth = stx.Btime.Sec*1_000_000_000 + int64(stx.Btime.Nsec)
	}
	if generation, ok := inodeGeneration(path, id.Ino); ok {
		id.Generation, id.ModTime = uint64(generation), 0
	} else if generation, ok := fileHandleGeneration(path, id.Ino); ok {
		id.Generation, id.ModTime = generation, 0
	}
	return id
}

func fileHandleGeneration(path string, ino uint64) (uint64, bool) {
	handle, _, err := nameToHandleAt(unix.AT_FDCWD, path, 0)
	if err != nil {
		return 0, false
	}
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || st.Ino != ino {
		return 0, false
	}
	var fsStats unix.Statfs_t
	if unix.Statfs(path, &fsStats) != nil {
		return 0, false
	}
	// FNV-1a keeps the persisted identity compact while covering the stable
	// filesystem id, handle type, inode and opaque generation-bearing bytes.
	// The mount id from name_to_handle_at is deliberately excluded: it changes
	// after a remount even though the file handle still names the same object.
	hash := uint64(14695981039346656037)
	mix := func(value uint64) {
		for range 8 {
			hash ^= uint64(byte(value))
			hash *= 1099511628211
			value >>= 8
		}
	}
	metadata := strconv.AppendInt(nil, int64(fsStats.Fsid.Val[0]), 10)
	metadata = append(metadata, 0)
	metadata = strconv.AppendInt(metadata, int64(fsStats.Fsid.Val[1]), 10)
	metadata = append(metadata, 0)
	metadata = strconv.AppendInt(metadata, int64(handle.Type()), 10)
	for _, value := range metadata {
		hash ^= uint64(value)
		hash *= 1099511628211
	}
	mix(ino)
	for _, value := range handle.Bytes() {
		hash ^= uint64(value)
		hash *= 1099511628211
	}
	if hash == 0 {
		hash = 1
	}
	return hash, true
}

func inodeGeneration(path string, ino uint64) (uint32, bool) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		return 0, false
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Ino != ino {
		return 0, false
	}
	generation, err := unix.IoctlGetUint32(fd, fsIOCGetVersion)
	return generation, err == nil && generation != 0
}
