//go:build darwin

package errhance

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

func bsdReadAttrs(fd int) (map[string]string, error) {
	// macOS ACLs are not ordinary listxattr entries. fgetattrlist returns an
	// attrreference with length zero when no extended ACL exists. Reject any
	// nonempty security record rather than silently dropping an ACL. x/sys has
	// no fgetattrlist wrapper; use its Darwin syscall number and Attrlist ABI.
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var security [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&security[0])), uintptr(len(security)), unix.FSOPT_REPORT_FULLSIZE, 0)
	if errno != 0 {
		return nil, fmt.Errorf("cannot inspect macOS ACL: %w", errno)
	}
	size := binary.LittleEndian.Uint32(security[:4])
	if size < 12 || size > uint32(len(security)) {
		return nil, fmt.Errorf("unsupported macOS security record")
	}
	if binary.LittleEndian.Uint32(security[8:12]) != 0 {
		return nil, fmt.Errorf("unsupported macOS extended ACL")
	}
	sizeBytes, err := unix.Flistxattr(fd, nil)
	if err == unix.ENOTSUP {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sizeBytes == 0 {
		return nil, nil
	}
	list := make([]byte, sizeBytes)
	sizeBytes, err = unix.Flistxattr(fd, list)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, name := range strings.Split(string(list[:sizeBytes]), "\x00") {
		if name == "" {
			continue
		}
		// Quarantine, Finder metadata, tags, and user attributes are ordinary
		// xattrs and are copied exactly. Do not copy opaque security/compression
		// state or a resource fork as though it were just a source annotation.
		if strings.HasPrefix(name, "com.apple.system.") || strings.HasPrefix(name, "com.apple.rootless") || name == "com.apple.macl" || name == "com.apple.provenance" || name == "com.apple.decmpfs" || name == "com.apple.ResourceFork" {
			return nil, fmt.Errorf("unsupported macOS extended metadata %q", name)
		}
		size, err := unix.Fgetxattr(fd, name, nil)
		if err != nil {
			return nil, err
		}
		value := make([]byte, size)
		size, err = unix.Fgetxattr(fd, name, value)
		if err != nil {
			return nil, err
		}
		if size > len(value) {
			return nil, fmt.Errorf("extended metadata %q changed while reading", name)
		}
		result[name] = string(value[:size])
	}
	return result, nil
}

func bsdApplyAttrs(fd int, current, original map[string]string) error {
	for name := range current {
		if _, ok := original[name]; !ok {
			if err := unix.Fremovexattr(fd, name); err != nil {
				return err
			}
		}
	}
	for name, value := range original {
		if err := unix.Fsetxattr(fd, name, []byte(value), 0); err != nil {
			return err
		}
	}
	return nil
}
