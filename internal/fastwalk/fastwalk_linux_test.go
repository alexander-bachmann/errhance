package fastwalk

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWalkFIFO(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pipe.go")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	// FIFOs must never reach callbacks that may open them and block forever.
	if got := collect(t, root); len(got) != 0 {
		t.Fatal(got)
	}
	if err := Walk(NewConfig(func(context.Context, string) error {
		t.Error("callback invoked for FIFO root")
		return nil
	}, WithRootFilepath(path))); err == nil {
		t.Fatal("FIFO root accepted")
	}
}
