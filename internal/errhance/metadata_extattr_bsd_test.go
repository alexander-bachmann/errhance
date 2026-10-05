//go:build freebsd || netbsd

package errhance

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestBSDRejectsExtendedAttributes(t *testing.T) {
	path, original := metadataFixture(t)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	value := []byte("do not discard")
	var pinned runtime.Pinner
	pinned.Pin(&value[0])
	defer pinned.Unpin()
	_, err = unix.ExtattrSetFd(int(file.Fd()), unix.EXTATTR_NAMESPACE_USER, "errhance-test", uintptr(unsafe.Pointer(&value[0])), len(value))
	if err == unix.ENOTSUP {
		t.Skip("filesystem lacks extended attributes")
	}
	if err != nil {
		t.Fatal(err)
	}
	committed, err := atomicReplace(context.Background(), path, original, []byte("package changed\n"))
	if err == nil || committed || !strings.Contains(err.Error(), "extended") {
		t.Fatalf("BSD metadata accepted: committed=%v error=%v", committed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original.data) {
		t.Fatal("rejected metadata replacement changed source")
	}
}
