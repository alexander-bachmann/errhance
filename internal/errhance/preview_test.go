package errhance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewMatchesWrittenOutput(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "returns", true: "overwrite"}[overwrite], func(t *testing.T) {
			src := validSource
			if overwrite {
				src = strings.Replace(src, `import "errors"`, `import ("errors"; "fmt")`, 1)
				src = strings.Replace(src, "return problem", `return fmt.Errorf("original: %w", problem)`, 1)
			}
			root := projectFixture(t, map[string]string{"z/z.go": src, "a/a.go": src})
			before := make(map[string]os.FileInfo)
			for _, name := range []string{"go.mod", "a/a.go", "z/z.go"} {
				path := filepath.Join(root, name)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = info
			}
			report, err := LocalDirectory(projectDirectory(root), NewSourceConfig(WithDryRun(true), WithOverwrite(overwrite)))
			if err != nil {
				t.Fatal(err)
			}
			if report.TotalFilesModified != 0 || report.TotalErrorsEnhanced != 0 || report.ProposedFilesModified != 2 || report.ProposedErrorsEnhanced != 2 || len(report.Changes) != 2 {
				t.Fatalf("unexpected preview: %+v", report)
			}
			if report.Changes[0].Path != filepath.Join(root, "a/a.go") || report.Changes[1].Path != filepath.Join(root, "z/z.go") {
				t.Fatalf("changes not sorted: %+v", report.Changes)
			}
			for _, change := range report.Changes {
				if change.Before != src || readProjectFile(t, change.Path) != src || change.After == src {
					t.Fatalf("preview wrote source or lacks proposed output: %+v", change)
				}
			}
			for path, info := range before {
				after, err := os.Stat(path)
				if err != nil || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || info.Mode() != after.Mode() {
					t.Fatalf("preview changed metadata for %s: %v", path, err)
				}
			}
			written, err := LocalDirectory(projectDirectory(root), NewSourceConfig(WithOverwrite(overwrite)))
			if err != nil || written.TotalFilesModified != report.ProposedFilesModified || written.TotalErrorsEnhanced != report.ProposedErrorsEnhanced {
				t.Fatalf("write differs from preview: %+v, %v", written, err)
			}
			for _, change := range report.Changes {
				if readProjectFile(t, change.Path) != change.After {
					t.Fatalf("written output differs from preview: %s", change.Path)
				}
			}
		})
	}
}

func TestLocalFilePreviewReadOnly(t *testing.T) {
	root := projectFixture(t, map[string]string{"p.go": validSource})
	path := filepath.Join(root, "p.go")
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0640)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := LocalFile(NewSourceConfig(WithDryRun(true)), path)
	if err != nil || report.TotalErrorsEnhanced != 1 || report.After == validSource {
		t.Fatalf("preview failed: %+v, %v", report, err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() || readProjectFile(t, path) != validSource {
		t.Fatalf("preview changed read-only file: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("preview created files: %v, %v", entries, err)
	}
}

func TestPreviewFailureHasNoChanges(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "invalid package"}[invalid], func(t *testing.T) {
			files := map[string]string{"p.go": validSource}
			if invalid {
				files["broken.go"] = "package p; var _ = missing"
			}
			root := projectFixture(t, files)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !invalid {
				cancel()
			}
			report, err := LocalDirectoryContext(ctx, projectDirectory(root), NewSourceConfig(WithDryRun(true)))
			if err == nil || report.TotalFilesModified != 0 || report.ProposedFilesModified != 0 || report.ProposedErrorsEnhanced != 0 || len(report.Changes) != 0 {
				t.Fatalf("failed preview reported changes: %+v, %v", report, err)
			}
			if readProjectFile(t, filepath.Join(root, "p.go")) != validSource {
				t.Fatal("failed preview changed source")
			}
		})
	}
}
