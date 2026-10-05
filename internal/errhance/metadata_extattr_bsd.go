//go:build freebsd || netbsd

package errhance

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// BSD extended attributes are not portable xattrs. Ordinary files are supported;
// enumerated attributes are rejected rather than discarded. As on Linux,
// privileged metadata hidden from this process is outside the guarantee.
func bsdReadAttrs(fd int) (map[string]string, error) {
	if err := bsdCheckACL(fd); err != nil {
		return nil, err
	}
	for _, namespace := range []int{unix.EXTATTR_NAMESPACE_USER, unix.EXTATTR_NAMESPACE_SYSTEM} {
		size, err := unix.ExtattrListFd(fd, namespace, 0, 0)
		if err == unix.ENOTSUP {
			continue
		}
		if namespace == unix.EXTATTR_NAMESPACE_SYSTEM && (err == unix.EPERM || err == unix.EACCES) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if size != 0 {
			return nil, fmt.Errorf("unsupported BSD extended attributes in namespace %d", namespace)
		}
	}
	return nil, nil
}

func bsdApplyAttrs(_ int, _, _ map[string]string) error { return nil }
