//go:build linux

package errhance

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAtomicReplaceLinuxMetadata(t *testing.T) {
	root := projectFixture(t, map[string]string{"p.go": validSource})
	path := filepath.Join(root, "p.go")
	if err := unix.Setxattr(path, "user.errhance-test", []byte("value\x00binary"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("filesystem lacks xattrs")
		}
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "user.empty", nil, 0); err != nil {
		t.Fatal(err)
	}
	// Linux POSIX ACL xattr format: version followed by tag/permissions/id.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	entries := []struct {
		tag, perm uint16
		id        uint32
	}{
		{1, 6, ^uint32(0)}, {2, 4, uint32(os.Getuid() + 1)},
		{4, 4, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)},
	}
	for i, entry := range entries {
		binary.LittleEndian.PutUint16(acl[4+i*8:], entry.tag)
		binary.LittleEndian.PutUint16(acl[6+i*8:], entry.perm)
		binary.LittleEndian.PutUint32(acl[8+i*8:], entry.id)
	}
	if err := unix.Setxattr(path, "system.posix_acl_access", acl, 0); err != nil {
		if !errors.Is(err, unix.ENOTSUP) {
			t.Fatal(err)
		}
		t.Log("filesystem lacks ACLs; user xattr preservation still tested")
	}
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := readFileMetadata(path, info)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := atomicReplace(context.Background(), path, projectSnapshot{data: []byte(validSource), info: info, metadata: &before}, []byte("package p\n"))
	if err != nil || !committed {
		t.Fatalf("committed=%v, error=%v", committed, err)
	}
	newInfo, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readFileMetadata(path, newInfo)
	if err != nil || !sameFileMetadata(before, after) || newInfo.Mode() != info.Mode() {
		t.Fatalf("metadata lost: before=%+v after=%+v error=%v", before, after, err)
	}
}

func TestAtomicReplaceMetadataChanged(t *testing.T) {
	root := projectFixture(t, map[string]string{"p.go": validSource})
	path := filepath.Join(root, "p.go")
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := readFileMetadata(path, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "user.changed", []byte("changed"), 0); err != nil {
		t.Skip(err)
	}
	committed, err := atomicReplace(context.Background(), path, projectSnapshot{data: []byte(validSource), info: info, metadata: &metadata}, []byte("package p\n"))
	if err == nil || committed || !strings.Contains(err.Error(), "metadata changed") {
		t.Fatalf("metadata race accepted: committed=%v error=%v", committed, err)
	}
	if readProjectFile(t, path) != validSource {
		t.Fatal("metadata race changed original")
	}
}

func TestLinuxMetadataDoesNotChangeOwnership(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	err = applyFileMetadata(file, fileMetadata{uid: stat.Uid + 1, gid: stat.Gid}, info.Mode())
	if err == nil || !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("owner mismatch accepted: %v", err)
	}
	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if after.Sys().(*syscall.Stat_t).Uid != stat.Uid {
		t.Fatal("changed owner unsafely")
	}
}

func TestProjectRejectsUnsupportedMetadata(t *testing.T) {
	root := projectFixture(t, map[string]string{"a/a.go": validSource, "b/b.go": validSource})
	path := filepath.Join(root, "b/b.go")
	capability := make([]byte, 20)
	binary.LittleEndian.PutUint32(capability, 0x02000001)
	binary.LittleEndian.PutUint32(capability[4:], 1<<10)
	if err := unix.Setxattr(path, "security.capability", capability, 0); err != nil {
		t.Skipf("cannot create security metadata: %v", err)
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err == nil || !strings.Contains(err.Error(), "unsupported extended metadata") || report.TotalFilesModified != 0 {
		t.Fatalf("unsupported metadata accepted: %+v, %v", report, err)
	}
	if readProjectFile(t, path) != validSource || readProjectFile(t, filepath.Join(root, "a/a.go")) != validSource {
		t.Fatal("metadata preflight failure wrote files")
	}
}
