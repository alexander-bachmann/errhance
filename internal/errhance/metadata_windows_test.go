//go:build windows

package errhance

import (
	"context"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsProtectedDACL(t *testing.T) {
	path, original := metadataFixture(t)
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	// A non-inherited owner-only ACL must survive replacement, not be replaced
	// with the parent directory's default grants.
	restricted, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + owner.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := restricted.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	metadata, err := readFileMetadata(path, original.info)
	if err != nil {
		t.Fatal(err)
	}
	if !metadata.protected {
		t.Fatal("test DACL not protected")
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
		t.Fatalf("DACL lost: %+v -> %+v, %v", metadata, after, err)
	}
}

func TestWindowsRejectsAlternateStreams(t *testing.T) {
	path, original := metadataFixture(t)
	if err := os.WriteFile(path+":errhance", []byte("do not discard"), 0600); err != nil {
		t.Skipf("filesystem lacks ADS: %v", err)
	}
	committed, err := atomicReplace(context.Background(), path, original, []byte("package changed\n"))
	if err == nil || committed || !strings.Contains(err.Error(), "alternate data streams") {
		t.Fatalf("ADS accepted: committed=%v error=%v", committed, err)
	}
	data, err := os.ReadFile(path + ":errhance")
	if err != nil || string(data) != "do not discard" {
		t.Fatalf("ADS lost: %q, %v", data, err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != string(original.data) {
		t.Fatal("rejected replacement changed source")
	}
}

func TestWindowsFileAttributes(t *testing.T) {
	path, original := metadataFixture(t)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	flags := uint32(windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED)
	if err := windows.SetFileAttributes(name, flags); err != nil {
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
	after, err := windows.GetFileAttributes(name)
	if err != nil || after != flags {
		t.Fatalf("attributes changed: %#x, %v", after, err)
	}
}
