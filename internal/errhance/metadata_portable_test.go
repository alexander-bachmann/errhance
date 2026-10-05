//go:build darwin || windows || freebsd || netbsd || openbsd

package errhance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Resolve the test root because macOS's temporary root commonly traverses /var,
// a symlink. Production deliberately requires non-symlink ancestor paths.
func metadataFixture(t *testing.T) (string, projectSnapshot) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "p.go")
	data := []byte("package p\n")
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatal(err)
	}
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, projectSnapshot{data: data, info: info}
}

func TestPlatformMetadataReplacement(t *testing.T) {
	path, original := metadataFixture(t)
	metadata, err := readFileMetadata(path, original.info)
	if err != nil {
		t.Fatal(err)
	}
	original.metadata = &metadata
	want := []byte("package p\nvar X = 1\n")
	committed, err := atomicReplace(context.Background(), path, original, want)
	if err != nil || !committed {
		t.Fatalf("committed=%v error=%v", committed, err)
	}
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readFileMetadata(path, info)
	if err != nil || !sameFileMetadata(metadata, after) || info.Mode() != original.info.Mode() {
		t.Fatalf("metadata lost: %+v -> %+v, %v", metadata, after, err)
	}
	if os.SameFile(info, original.info) {
		t.Fatal("replacement did not replace inode/file identity")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(want) {
		t.Fatalf("bad replacement: %q, %v", data, err)
	}
}

func TestPlatformMetadataHardLinks(t *testing.T) {
	path, _ := metadataFixture(t)
	if err := os.Link(path, path+".link"); err != nil {
		t.Skip(err)
	}
	if _, err := safeFile(path); err == nil {
		t.Fatal("hard link accepted")
	}
}
