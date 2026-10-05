package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexander-bachmann/errhance/internal/errhance"
	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

func TestRejectPositionalTargets(t *testing.T) {
	if err := rootCmd.ValidateArgs([]string{"one.go"}); err == nil {
		t.Fatal("positional targets must not silently select the current directory")
	}
	if err := rootCmd.ValidateArgs(nil); err != nil {
		t.Fatal(err)
	}
}

const cliSource = `package example

import "errors"

func source() error { return errors.New("failure") }

func F() error {
	err := source()
	if err != nil {
		return err
	}
	return nil
}
`

// Go 1.22 has no testing.T.Chdir. These tests must not run in parallel.
func cliProject(t *testing.T, source string) string {
	t.Helper()
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	root := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/cli\n\ngo 1.22.5\n",
		"foo.go": source,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Error(err)
		}
	})
	return root
}

func TestCLIChanges(t *testing.T) {
	for _, tc := range []struct {
		name, source, summary string
		args                  []string
		diff, write, fail     bool
	}{
		{name: "normal", source: cliSource, summary: "1 file modified, 1 error enhanced\n", write: true},
		{name: "dry run", source: cliSource, args: []string{"--dry-run"}, summary: "1 file would be modified, 1 error would be enhanced\n"},
		{name: "diff", source: cliSource, args: []string{"--diff"}, diff: true, summary: "1 file would be modified, 1 error would be enhanced\n"},
		{name: "diff overrides false", source: cliSource, args: []string{"--diff", "--dry-run=false"}, diff: true, summary: "1 file would be modified, 1 error would be enhanced\n"},
		{name: "no changes", source: "package example\n", args: []string{"--diff"}, summary: "0 files would be modified, 0 errors would be enhanced\n"},
		{name: "invalid package", source: cliSource + "\nvar broken = missing\n", args: []string{"--diff"}, summary: "0 files would be modified, 0 errors would be enhanced\n", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliProject(t, tc.source)
			before, err := os.Stat(filepath.Join(root, "foo.go"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := newRootCmd()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append([]string{}, tc.args...))
			cmd.SilenceErrors = true
			err = cmd.Execute()
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v, stderr=%s", err, &stderr)
			}
			if len(tc.args) > 0 && tc.args[0] == "--diff" {
				if !strings.Contains(stderr.String(), tc.summary) {
					t.Errorf("stderr=%q, want summary %q", &stderr, tc.summary)
				}
				if tc.diff {
					if !strings.HasPrefix(stdout.String(), "--- a/foo.go\n+++ b/foo.go\n@@ ") || !strings.Contains(stdout.String(), `+`+"\t\t"+`return errhancefmt.Errorf("source: %w", err)`) {
						t.Errorf("unexpected diff: %s", &stdout)
					}
					if strings.Contains(stdout.String(), "would be") || strings.Contains(stdout.String(), root) {
						t.Errorf("stdout contains non-patch output: %s", &stdout)
					}
				} else if stdout.Len() != 0 {
					t.Errorf("expected empty stdout, got %q", &stdout)
				}
				if tc.fail && !strings.Contains(stderr.String(), "missing") {
					t.Errorf("missing diagnostic: %s", &stderr)
				}
			} else if stdout.String() != tc.summary {
				t.Errorf("stdout=%q, want %q", &stdout, tc.summary)
			}
			after, err := os.ReadFile(filepath.Join(root, "foo.go"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.write {
				if !strings.Contains(string(after), "fmt.Errorf") {
					t.Errorf("source not rewritten: %s", after)
				}
			} else {
				info, err := os.Stat(filepath.Join(root, "foo.go"))
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != tc.source || !os.SameFile(before, info) || !before.ModTime().Equal(info.ModTime()) {
					t.Error("preview changed source contents or metadata")
				}
			}
		})
	}
}

func TestCLIReportCounters(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "dry run"}[dryRun], func(t *testing.T) {
			root := cliProject(t, cliSource)
			dir := errhance.NewDirectoryConfig(fastwalk.NewConfig(nil, fastwalk.WithOnlyExtensions([]string{".go"})))
			report, err := errhance.LocalDirectoryContext(context.Background(), dir, errhance.NewSourceConfig(errhance.WithDryRun(dryRun)))
			if err != nil {
				t.Fatal(err)
			}
			if dryRun {
				if report.TotalFilesModified != 0 || report.TotalErrorsEnhanced != 0 || report.ProposedFilesModified != 1 || report.ProposedErrorsEnhanced != 1 || len(report.Changes) != 1 {
					t.Fatalf("wrong dry-run counters: %+v", report)
				}
				change := report.Changes[0]
				if change.Path != filepath.Join(root, "foo.go") || change.Before != cliSource || !strings.Contains(change.After, "fmt.Errorf") {
					t.Errorf("wrong change: %+v", change)
				}
			} else if report.TotalFilesModified != 1 || report.TotalErrorsEnhanced != 1 || len(report.Changes) != 0 {
				t.Errorf("wrong normal counters: %+v", report)
			}
		})
	}
}

func TestDiffCommandWriteErrors(t *testing.T) {
	cliProject(t, cliSource)
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			cmd := newRootCmd()
			cmd.SetArgs([]string{"--diff"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			if stream == "stdout" {
				cmd.SetOut(failedWriter{})
			} else {
				cmd.SetErr(failedWriter{})
			}
			if err := cmd.Execute(); !errors.Is(err, errOutput) {
				t.Fatalf("error=%v, want output error", err)
			}
		})
	}
}
