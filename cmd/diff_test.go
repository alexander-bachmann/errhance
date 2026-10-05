package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexander-bachmann/errhance/internal/errhance"
)

var errOutput = errors.New("output failed")

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errOutput }

func TestWriteDiff(t *testing.T) {
	for _, tc := range []struct{ name, before, after, want string }{
		{"context", "one\nold\nthree\n", "one\nnew\nthree\n", "@@ -1,3 +1,3 @@\n one\n-old\n+new\n three\n"},
		{"old EOF", "old", "new\n", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n"},
		{"new EOF", "old\n", "new", "@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n"},
		{"both EOF", "old", "new", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n"},
		{"context EOF", "old\nsame", "new\nsame", "@@ -1,2 +1,2 @@\n-old\n+new\n same\n\\ No newline at end of file\n"},
		{"newline only", "same", "same\n", "@@ -1 +1 @@\n-same\n\\ No newline at end of file\n+same\n"},
		{"empty old", "", "new\n", "@@ -0,0 +1 @@\n+new\n"},
		{"empty new", "old\n", "", "@@ -1 +0,0 @@\n-old\n"},
		{"unchanged", "same", "same", ""},
		{"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			var out bytes.Buffer
			if err := writeDiff(&out, root, []errhance.FileChange{{Path: filepath.Join(root, "foo.go"), Before: tc.before, After: tc.after}}); err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if want != "" {
				want = "--- a/foo.go\n+++ b/foo.go\n" + want
			}
			if out.String() != want {
				t.Errorf("diff=%q, want %q", out.String(), want)
			}
		})
	}
}

func TestDiffPathsAndOrder(t *testing.T) {
	root := t.TempDir()
	changes := []errhance.FileChange{
		{Path: filepath.Join(root, "z.go"), Before: "old\n", After: "new\n"},
		{Path: filepath.Join(root, "sub", "a file.go"), Before: "old\n", After: "new\n"},
	}
	var out bytes.Buffer
	if err := writeDiff(&out, root, changes); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "--- \"a/sub/a file.go\"\n+++ \"b/sub/a file.go\"\n") || !strings.Contains(out.String(), "--- a/z.go\n+++ b/z.go\n") {
		t.Errorf("wrong paths/order: %s", &out)
	}
	if changes[0].Path != filepath.Join(root, "z.go") {
		t.Error("mutated input order")
	}
	for _, tc := range []struct{ path, want string }{
		{"a/plain.go", "a/plain.go"},
		{"a/a b.go", `"a/a b.go"`},
		{"a/a\tb\nc\rd.go", `"a/a\tb\nc\rd.go"`},
		{"a/a\"b\\c.go", `"a/a\"b\\c.go"`},
		{"a/\x01\x7f\xc3\xa9.go", `"a/\001\177\303\251.go"`},
	} {
		if got := diffPath(tc.path); got != tc.want {
			t.Errorf("diffPath(%q)=%q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestDiffWriteError(t *testing.T) {
	root := t.TempDir()
	err := writeDiff(failedWriter{}, root, []errhance.FileChange{{Path: filepath.Join(root, "foo.go"), Before: "old\n", After: "new\n"}})
	if !errors.Is(err, errOutput) {
		t.Fatalf("error=%v, want output error", err)
	}
}

func TestDiffApplies(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed; patch application check unavailable")
	}
	root := t.TempDir()
	changes := []errhance.FileChange{
		{Path: filepath.Join(root, "nested", "a file.go"), Before: "old", After: "new\n"},
		{Path: filepath.Join(root, "b.go"), Before: "one\nold\nsame", After: "one\nnew\nsame"},
		{Path: filepath.Join(root, "\xc3\xa9.go"), Before: "old\n", After: "new\n"},
	}
	for _, change := range changes {
		if err := os.MkdirAll(filepath.Dir(change.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(change.Path, []byte(change.Before), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := writeDiff(&out, root, changes); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(git, "apply", "--no-index", "--whitespace=nowarn", "-")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(out.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid unified diff: %v\n%s\n%s", err, output, out.String())
	}
	for _, change := range changes {
		got, err := os.ReadFile(change.Path)
		if err != nil || string(got) != change.After {
			t.Fatalf("patch output mismatch for %s: %q, %v", change.Path, got, err)
		}
	}
}
