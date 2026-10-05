//go:build linux

package errhance

import (
	"fmt"
	"maps"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type fileMetadata struct {
	uid, gid uint32
	xattrs   map[string]string
}

func checkFileLinks(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return fmt.Errorf("unsafe or unverifiable hard links for %s", path)
	}
	return nil
}

// Only user attributes and POSIX access ACLs are supported. Enumerated security
// labels, capabilities, and trusted attributes cause rejection before replacement.
// Metadata hidden by filesystem or privilege policy is outside this guarantee;
// this is not a privileged metadata migration tool.
func readFileMetadata(path string, original os.FileInfo) (fileMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileMetadata{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fileMetadata{}, err
	}
	old, ok := original.Sys().(*syscall.Stat_t)
	stat, valid := info.Sys().(*syscall.Stat_t)
	if !ok || !valid || !os.SameFile(original, info) || old.Uid != stat.Uid || old.Gid != stat.Gid {
		return fileMetadata{}, fmt.Errorf("file ownership or identity changed: %s", path)
	}
	attrs, err := linuxXattrs(int(file.Fd()))
	if err != nil {
		return fileMetadata{}, fmt.Errorf("metadata %s: %w", path, err)
	}
	return fileMetadata{uid: stat.Uid, gid: stat.Gid, xattrs: attrs}, nil
}

func linuxXattrs(fd int) (map[string]string, error) {
	size, err := unix.Flistxattr(fd, nil)
	if err == unix.ENOTSUP {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	list := make([]byte, size)
	size, err = unix.Flistxattr(fd, list)
	if err != nil {
		return nil, err
	}
	attrs := make(map[string]string)
	for _, name := range strings.Split(string(list[:size]), "\x00") {
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "user.") && name != "system.posix_acl_access" {
			return nil, fmt.Errorf("unsupported extended metadata %q", name)
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
		attrs[name] = string(value[:size])
	}
	return attrs, nil
}

func sameFileMetadata(a, b fileMetadata) bool {
	return a.uid == b.uid && a.gid == b.gid && maps.Equal(a.xattrs, b.xattrs)
}

func applyFileMetadata(file *os.File, original fileMetadata, mode os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != original.uid || stat.Gid != original.gid {
		return fmt.Errorf("cannot preserve ownership of %s without changing owner/group", file.Name())
	}
	attrs, err := linuxXattrs(int(file.Fd()))
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	// Remove inherited ACLs/attributes absent from the original, then restore
	// exact values after chmod (chmod itself can alter a POSIX ACL's mask).
	for name := range attrs {
		if _, ok := original.xattrs[name]; !ok {
			if err := unix.Fremovexattr(int(file.Fd()), name); err != nil {
				return err
			}
		}
	}
	for name, value := range original.xattrs {
		if err := unix.Fsetxattr(int(file.Fd()), name, []byte(value), 0); err != nil {
			return err
		}
	}
	info, err = file.Stat()
	if err != nil {
		return err
	}
	attrs, err = linuxXattrs(int(file.Fd()))
	if err != nil {
		return err
	}
	if info.Mode() != mode || !maps.Equal(attrs, original.xattrs) {
		return fmt.Errorf("cannot preserve access metadata of %s", file.Name())
	}
	return nil
}
