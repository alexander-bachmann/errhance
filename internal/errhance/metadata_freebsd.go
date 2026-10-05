//go:build freebsd

package errhance

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

func bsdCheckACL(fd int) error {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return err
	}
	// FreeBSD's struct acl begins with acl_maxcnt and acl_cnt, followed by
	// reserved words and up to 254 entries. A three-entry POSIX ACL is exactly
	// the mode's owner/group/other permissions. NFSv4 ACLs are not reconstructed.
	for _, kind := range []uintptr{2, 4} { // ACL_TYPE_ACCESS, ACL_TYPE_NFS4
		if kind == 2 && fs.Flags&unix.MNT_ACLS == 0 {
			continue
		}
		if kind == 4 && fs.Flags&unix.MNT_NFS4ACLS == 0 {
			continue
		}
		var acl [2048]uint32
		acl[0] = 254
		_, _, errno := unix.Syscall(unix.SYS___ACL_GET_FD, uintptr(fd), kind, uintptr(unsafe.Pointer(&acl[0])))
		if errno != 0 {
			return fmt.Errorf("cannot inspect FreeBSD ACL: %w", errno)
		}
		if kind == 4 || acl[1] != 3 {
			return fmt.Errorf("unsupported FreeBSD extended ACL")
		}
	}
	return nil
}
