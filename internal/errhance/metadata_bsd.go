//go:build darwin || freebsd || netbsd || openbsd

package errhance

import (
	"fmt"
	"maps"
	"os"
	"syscall"
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

// Ownership is never changed to make an edit possible. Nonzero BSD file flags
// and unsupported access metadata are rejected, on both original and temp file.
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
	if stat.Flags != 0 {
		return fileMetadata{}, fmt.Errorf("unsupported BSD file flags on %s", path)
	}
	attrs, err := bsdReadAttrs(int(file.Fd()))
	if err != nil {
		return fileMetadata{}, fmt.Errorf("metadata %s: %w", path, err)
	}
	return fileMetadata{uid: stat.Uid, gid: stat.Gid, xattrs: attrs}, nil
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
	if stat.Flags != 0 {
		return fmt.Errorf("unsupported BSD file flags on %s", file.Name())
	}
	// Checking the temp also detects inherited ACLs; chmod cannot remove them.
	attrs, err := bsdReadAttrs(int(file.Fd()))
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := bsdApplyAttrs(int(file.Fd()), attrs, original.xattrs); err != nil {
		return err
	}
	info, err = file.Stat()
	if err != nil {
		return err
	}
	after, err := readFileMetadata(file.Name(), info)
	if err != nil {
		return err
	}
	if info.Mode() != mode || !sameFileMetadata(original, after) {
		return fmt.Errorf("cannot preserve access metadata of %s", file.Name())
	}
	return nil
}
