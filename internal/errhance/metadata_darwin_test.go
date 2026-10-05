//go:build darwin

package errhance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinXattrs(t *testing.T) {
	path, original := metadataFixture(t)
	if err := unix.Setxattr(path, "com.errhance.test", []byte("value\x00binary"), 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "com.errhance.empty", nil, 0); err != nil {
		t.Fatal(err)
	}
	metadata, err := readFileMetadata(path, original.info)
	if err != nil {
		t.Fatal(err)
	}
	original.metadata = &metadata
	committed, err := atomicReplace(context.Background(), path, original, []byte("package p\nvar X = 1\n"))
	if err != nil || !committed {
		t.Fatalf("committed=%v error=%v", committed, err)
	}
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readFileMetadata(path, info)
	if err != nil || !sameFileMetadata(metadata, after) {
		t.Fatalf("xattrs lost: %+v -> %+v, %v", metadata, after, err)
	}
}

func TestDarwinRejectsACL(t *testing.T) {
	path, original := metadataFixture(t)
	entry := fmt.Sprintf("user:%d allow read", os.Getuid())
	if output, err := exec.Command("chmod", "+a", entry, path).CombinedOutput(); err != nil {
		t.Fatalf("set test ACL: %v: %s", err, output)
	}
	committed, err := atomicReplace(context.Background(), path, original, []byte("package changed\n"))
	if err == nil || committed || !strings.Contains(err.Error(), "ACL") {
		t.Fatalf("ACL accepted: committed=%v error=%v", committed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original.data) {
		t.Fatal("rejected ACL replacement changed source")
	}
}

func TestDarwinRejectsResourceFork(t *testing.T) {
	path, original := metadataFixture(t)
	if err := unix.Setxattr(path, "com.apple.ResourceFork", []byte("resource data"), 0); err != nil {
		t.Fatal(err)
	}
	committed, err := atomicReplace(context.Background(), path, original, []byte("package changed\n"))
	if err == nil || committed || !strings.Contains(err.Error(), "ResourceFork") {
		t.Fatalf("resource fork accepted: committed=%v error=%v", committed, err)
	}
}
