package fastwalk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	// macOS temporary directories can have symlink ancestors such as /var.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFiles(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func collect(t *testing.T, root string, opts ...Option) []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	cfg := NewConfig(func(_ context.Context, path string) error {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Errorf("callback path not normalized: %q", path)
		}
		mu.Lock()
		got = append(got, path)
		mu.Unlock()
		return nil
	}, append([]Option{WithRootFilepath(root)}, opts...)...)
	if err := Walk(cfg); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	return got
}

func TestWalkFilters(t *testing.T) {
	root := canonicalTempDir(t)
	writeFiles(t, root, "a.go", "b.txt", ".hidden.go", ".hidden/c.go", "sub/d.go", "vendor/e.go", "other/vendor", "other/z.go")
	tests := []struct {
		name string
		opts []Option
		want []string
	}{
		{"default", nil, []string{"a.go", "b.txt", "sub/d.go", "vendor/e.go", "other/vendor", "other/z.go"}},
		{"hidden", []Option{WithIncludeHidden(true)}, []string{"a.go", "b.txt", ".hidden.go", ".hidden/c.go", "sub/d.go", "vendor/e.go", "other/vendor", "other/z.go"}},
		{"extensions", []Option{WithOnlyExtensions([]string{"go"})}, []string{"a.go", "sub/d.go", "vendor/e.go", "other/z.go"}},
		// A regular file named vendor must not trigger SkipDir or skip z.go.
		{"skip directories", []Option{WithSkipDirectories([]string{"vendor"})}, []string{"a.go", "b.txt", "sub/d.go", "other/vendor", "other/z.go"}},
		{"directories", []Option{WithIncludeDirectories(true)}, []string{".", "a.go", "b.txt", "sub", "sub/d.go", "vendor", "vendor/e.go", "other", "other/vendor", "other/z.go"}},
		{"nested path", []Option{WithOnlyFilepaths([]string{filepath.Join(root, "sub/d.go")})}, []string{"sub/d.go"}},
		{"intersection", []Option{WithOnlyFilepaths([]string{filepath.Join(root, "b.txt")}), WithOnlyExtensions([]string{".go"})}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collect(t, root, tt.opts...)
			var want []string
			for _, name := range tt.want {
				want = append(want, filepath.Join(root, name))
			}
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestWalkUnderscoreDirectories(t *testing.T) {
	root := canonicalTempDir(t)
	writeFiles(t, root, "a.go", "_examples/b.go", "sub/_internal/c.go", "sub/_file.go")
	if got := collect(t, root, WithSkipUnderscoreDirectories(true)); !reflect.DeepEqual(got, []string{filepath.Join(root, "a.go"), filepath.Join(root, "sub/_file.go")}) {
		t.Fatalf("unexpected underscore filtering: %v", got)
	}
	if got := collect(t, filepath.Join(root, "_examples"), WithSkipUnderscoreDirectories(true)); !reflect.DeepEqual(got, []string{filepath.Join(root, "_examples/b.go")}) {
		t.Fatalf("explicit underscore root skipped: %v", got)
	}
	if got := collect(t, root); len(got) != 4 {
		t.Fatalf("option changed default walking: %v", got)
	}
}

func TestWalkNormalizedPaths(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := canonicalTempDir(t)
	writeFiles(t, root, "a.go", "sub/b.go")
	rel, err := filepath.Rel(wd, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{root, rel, rel + string(filepath.Separator) + "."} {
		for _, filter := range []string{filepath.Join(rel, "a.go"), filepath.Join(wd, rel) + "/sub/../a.go"} {
			got := collect(t, root, WithOnlyFilepaths([]string{filter}))
			want, err := filepath.Abs(filepath.Join(root, "a.go"))
			if err != nil || !reflect.DeepEqual(got, []string{want}) {
				t.Fatalf("root %q filter %q: got %v, want %s (err %v)", root, filter, got, want, err)
			}
		}
	}
	got := collect(t, ".", WithOnlyFilepaths([]string{"./fastwalk_test.go"}))
	if want := []string{filepath.Join(wd, "fastwalk_test.go")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root '.': got %v, want %v", got, want)
	}
}

func TestWalkHiddenRoot(t *testing.T) {
	root := filepath.Join(canonicalTempDir(t), ".root")
	writeFiles(t, root, "a.go", ".hidden.go", ".hidden/b.go")
	if got := collect(t, root); !reflect.DeepEqual(got, []string{filepath.Join(root, "a.go")}) {
		t.Fatal(got)
	}
	// An explicitly requested hidden regular file is also a valid root.
	path := filepath.Join(root, ".hidden.go")
	if got := collect(t, path); !reflect.DeepEqual(got, []string{path}) {
		t.Fatal(got)
	}
}

func TestWalkValidationAndErrors(t *testing.T) {
	noop := func(context.Context, string) error { return nil }
	for _, cfg := range []Config{{}, NewConfig(nil), NewConfig(noop, WithNumWorkers(0)), NewConfig(noop, WithNumWorkers(-1))} {
		if err := Walk(cfg); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	root := canonicalTempDir(t)
	if got := collect(t, root); len(got) != 0 {
		t.Fatal(got)
	}
	if err := Walk(NewConfig(noop, WithRootFilepath(filepath.Join(root, "missing")))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root: %v", err)
	}
	writeFiles(t, root, "a.go")
	want := errors.New("callback failed")
	if err := Walk(NewConfig(func(context.Context, string) error { return want }, WithRootFilepath(root))); !errors.Is(err, want) {
		t.Fatalf("callback error: %v", err)
	}
}

func TestWalkCancellation(t *testing.T) {
	root := canonicalTempDir(t)
	for i := 0; i < 250; i++ {
		writeFiles(t, root, fmt.Sprintf("%03d.go", i))
	}
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("scoped=%t", scoped), func(t *testing.T) {
			for _, mode := range []string{"pre-canceled", "deadline", "active", "callback error", "callback cancels"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					want := error(context.Canceled)
					if mode == "pre-canceled" {
						cancel()
					}
					if mode == "deadline" {
						var stop context.CancelFunc
						ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
						defer stop()
						want = context.DeadlineExceeded
					}
					if mode == "callback error" {
						want = errors.New("worker failed")
					}
					started := make(chan struct{})
					var calls atomic.Int32
					var active atomic.Int32
					cfg := NewConfig(func(ctx context.Context, _ string) error {
						calls.Add(1)
						active.Add(1)
						defer active.Add(-1)
						close(started)
						switch mode {
						case "active":
							<-ctx.Done()
						case "callback error":
							return want
						case "callback cancels":
							cancel()
						}
						return nil
					}, WithRootFilepath(root), WithNumWorkers(1))
					if scoped {
						WithOnlyFilepaths([]string{filepath.Join(root, "000.go")})(&cfg)
					}
					done := make(chan error, 1)
					go func() { done <- WalkContext(ctx, cfg) }()
					if mode == "active" {
						select {
						case <-started:
							cancel()
						case <-time.After(5 * time.Second):
							t.Fatal("callback did not start")
						}
					}
					select {
					case err := <-done:
						if !errors.Is(err, want) {
							t.Fatalf("got %v, want %v", err, want)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("walk did not stop")
					}
					if active.Load() != 0 || calls.Load() > 1 {
						t.Fatalf("active=%d calls=%d", active.Load(), calls.Load())
					}
					if (mode == "pre-canceled" || mode == "deadline") && calls.Load() != 0 {
						t.Fatal("callback ran with canceled context")
					}
				})
			}
		})
	}
}

func TestWalkSymlinks(t *testing.T) {
	root := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	writeFiles(t, root, "regular.go")
	writeFiles(t, outside, "outside.go")
	for name, target := range map[string]string{"file.go": filepath.Join(outside, "outside.go"), "dir": outside, "dangling": filepath.Join(outside, "missing"), "loop": root} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	// Never follow links, including dangling links and cycles, even when all
	// entry types and hidden names have been requested.
	got := collect(t, root, WithIncludeDirectories(true), WithIncludeHidden(true))
	if !reflect.DeepEqual(got, []string{root, filepath.Join(root, "regular.go")}) {
		t.Fatal(got)
	}
	var selected []string
	for _, name := range []string{"regular.go", "file.go", "dir", "dir/outside.go", "dangling", "loop", "loop/regular.go"} {
		selected = append(selected, filepath.Join(root, name))
	}
	if got := collect(t, root, WithOnlyFilepaths(selected), WithIncludeDirectories(true)); !reflect.DeepEqual(got, []string{filepath.Join(root, "regular.go")}) {
		t.Fatalf("scoped symlinks: %v", got)
	}
	for _, name := range []string{"file.go", "dir", "dangling", "loop", "dir/outside.go"} {
		cfg := NewConfig(func(context.Context, string) error {
			t.Error("callback invoked through root symlink")
			return nil
		}, WithRootFilepath(filepath.Join(root, name)))
		if err := Walk(cfg); err == nil {
			t.Fatalf("symlink root %s accepted", name)
		}
	}
}

func TestWalkSpecialFiles(t *testing.T) {
	root := canonicalTempDir(t)
	path := filepath.Join(root, "socket.go")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	defer listener.Close()
	writeFiles(t, root, "regular.go")
	// Special files are not callback inputs, even with a matching extension.
	if got := collect(t, root, WithOnlyExtensions([]string{"go"})); !reflect.DeepEqual(got, []string{filepath.Join(root, "regular.go")}) {
		t.Fatal(got)
	}
	if got := collect(t, root, WithOnlyFilepaths([]string{path})); len(got) != 0 {
		t.Fatalf("scoped special file: %v", got)
	}
	if err := Walk(NewConfig(func(context.Context, string) error {
		t.Error("callback invoked for socket root")
		return nil
	}, WithRootFilepath(path))); err == nil {
		t.Fatal("socket root accepted")
	}
}

func TestWalkDrainsQueue(t *testing.T) {
	root := canonicalTempDir(t)
	var want []string
	for i := 0; i < 250; i++ {
		name := fmt.Sprintf("%03d.go", i)
		writeFiles(t, root, name)
		want = append(want, filepath.Join(root, name))
	}
	for _, workers := range []int{1, 8} {
		if got := collect(t, root, WithNumWorkers(workers)); !reflect.DeepEqual(got, want) {
			t.Fatalf("workers=%d: queue not fully drained or duplicate callbacks", workers)
		}
	}
}

func TestWalkWaitsForWorkers(t *testing.T) {
	root := canonicalTempDir(t)
	writeFiles(t, root, "a.go", "b.go", "c.go", "d.go", "e.go")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 4)
	var completed atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- WalkContext(ctx, NewConfig(func(ctx context.Context, _ string) error {
			started <- struct{}{}
			<-ctx.Done()
			completed.Add(1)
			return nil
		}, WithRootFilepath(root), WithNumWorkers(4)))
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || completed.Load() != 4 {
			t.Fatalf("err=%v completed=%d", err, completed.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not stop")
	}
}

func TestWalkTraversalError(t *testing.T) {
	root := canonicalTempDir(t)
	blocked := filepath.Join(root, "blocked")
	writeFiles(t, root, "blocked/a.go")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0755) })
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("environment permits reading directories without permission")
	}
	err := Walk(NewConfig(func(context.Context, string) error {
		t.Error("callback invoked for inaccessible file")
		return nil
	}, WithRootFilepath(root)))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("got %v, want permission error", err)
	}
}

func TestWalkScopedTraversal(t *testing.T) {
	root := canonicalTempDir(t)
	writeFiles(t, root, "a.go", "sub/deep/b.go", "sub/c.go", ".hidden/d.go", "_examples/e.go", "vendor/f.go")
	tests := []struct {
		name  string
		root  string
		paths []string
		opts  []Option
		want  []string
	}{
		{"shared ancestors", ".", []string{"sub/deep/b.go", "sub/c.go", "missing/x.go"}, nil, []string{"sub/deep/b.go", "sub/c.go"}},
		{"skipped ancestors", ".", []string{"a.go", ".hidden/d.go", "_examples/e.go", "vendor/f.go"}, []Option{WithSkipUnderscoreDirectories(true), WithSkipDirectories([]string{"vendor"})}, []string{"a.go"}},
		{"hidden enabled", ".", []string{".hidden/d.go"}, []Option{WithIncludeHidden(true)}, []string{".hidden/d.go"}},
		{"directories", ".", []string{".", "sub", "sub/deep/b.go"}, []Option{WithIncludeDirectories(true)}, []string{".", "sub", "sub/deep/b.go"}},
		{"directory excluded", ".", []string{"sub"}, nil, nil},
		{"hidden root", ".hidden", []string{".hidden/d.go"}, nil, []string{".hidden/d.go"}},
		{"underscore root", "_examples", []string{"_examples/e.go"}, []Option{WithSkipUnderscoreDirectories(true)}, []string{"_examples/e.go"}},
		{"vendor root", "vendor", []string{"vendor/f.go"}, []Option{WithSkipDirectories([]string{"vendor"})}, nil},
		{"regular root", "a.go", []string{"a.go"}, nil, []string{"a.go"}},
		{"regular root mismatch", "a.go", []string{"sub/c.go"}, nil, nil},
		{"hidden regular root", ".hidden/d.go", []string{".hidden/d.go"}, nil, []string{".hidden/d.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths, want []string
			for _, path := range tt.paths {
				paths = append(paths, filepath.Join(root, path))
			}
			for _, path := range tt.want {
				want = append(want, filepath.Join(root, path))
			}
			sort.Strings(want)
			got := collect(t, filepath.Join(root, tt.root), append(tt.opts, WithOnlyFilepaths(paths))...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestWalkScopedUnreadableSibling(t *testing.T) {
	base := canonicalTempDir(t)
	root := filepath.Join(base, "project")
	writeFiles(t, root, "selected/deep/a.go", "irrelevant/b.go")
	writeFiles(t, base, "project-other/outside.go")
	blocked := filepath.Join(root, "irrelevant")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0755) })
	if _, err := os.ReadDir(blocked); !errors.Is(err, os.ErrPermission) {
		t.Skipf("environment does not enforce directory permissions: %v", err)
	}
	selected := filepath.Join(root, "selected/deep/a.go")
	outside := filepath.Join(base, "project-other/outside.go")
	for _, paths := range [][]string{{selected}, {selected, outside}, {outside}, {base}} {
		got := collect(t, root, WithOnlyFilepaths(paths), WithIncludeDirectories(true))
		var want []string
		if paths[0] == selected {
			want = []string{selected}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paths %v: got %v, want %v", paths, got, want)
		}
	}
	// Selecting the inaccessible subtree must still surface traversal errors.
	err := Walk(NewConfig(func(context.Context, string) error { return nil },
		WithRootFilepath(root), WithOnlyFilepaths([]string{filepath.Join(blocked, "b.go")})))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("got %v, want permission error", err)
	}
}
