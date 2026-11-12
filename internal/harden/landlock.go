package harden

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ErrNoLandlock means the kernel has no Landlock, or it's switched off
var ErrNoLandlock = errors.New("landlock isn't available")

// LockFilesystem denies every filesystem access Landlock knows about for
// the rest of the process's life, except allow under allowDir when
// allowDir isn't "". It needs no_new_privs, which the constructor sets.
// Call it once every file the process needs is open: a bug in a face
// model's decoder then gets a process that can't read or write anything.
func LockFilesystem(allowDir string, allow uint64) error {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno == unix.ENOSYS || errno == unix.EOPNOTSUPP {
		return ErrNoLandlock
	}
	if errno != 0 {
		return fmt.Errorf("landlock: %w", errno)
	}
	handled := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	// Later ABIs handle more; an older kernel would refuse the flags
	if abi >= 2 {
		handled |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		handled |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	attr := unix.LandlockRulesetAttr{Handled_access_fs: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock ruleset: %w", errno)
	}
	defer unix.Close(int(fd))

	if allowDir != "" {
		dfd, err := unix.Open(allowDir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err == nil {
			rule := unix.LandlockPathBeneathAttr{Allowed_access: allow & handled, Parent_fd: int32(dfd)}
			_, _, errno = unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH,
				uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
			unix.Close(dfd)
			if errno != 0 {
				return fmt.Errorf("landlock rule: %w", errno)
			}
		}
		// A missing directory gets no rule: whatever needed it fails later
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock restrict: %w", errno)
	}
	return nil
}

// SnapshotAccess is what writing snapshots into their directory needs
const SnapshotAccess = unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_WRITE_FILE
