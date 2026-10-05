//go:build !linux && !windows && !darwin && !freebsd && !netbsd && !openbsd

package errhance

import (
	"fmt"
	"os"
	"runtime"
)

type fileMetadata struct{}

// Disk entrypoints fail closed on platforms without a metadata implementation.
// The pure Source API remains available on these platforms.
func checkFileLinks(path string, _ os.FileInfo) error {
	return fmt.Errorf("disk metadata safety is unsupported on %s: %s", runtime.GOOS, path)
}

func readFileMetadata(path string, info os.FileInfo) (fileMetadata, error) {
	return fileMetadata{}, checkFileLinks(path, info)
}

func sameFileMetadata(_, _ fileMetadata) bool { return true }

func applyFileMetadata(_ *os.File, _ fileMetadata, _ os.FileMode) error {
	return fmt.Errorf("atomic replacement is unsupported on %s", runtime.GOOS)
}
