package errhance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestProjectLoadingPartial(t *testing.T) {
	for _, bad := range []string{"bad/bad.go", "bad/bad_test.go"} {
		t.Run(bad, func(t *testing.T) {
			files := map[string]string{
				"good/good.go":   validSource,
				"bad/good.go":    validSource,
				bad:              "package p; var _ = missing\n",
				"dependent/p.go": "package dependent\nimport _ \"example.com/project/bad\"\n" + strings.TrimPrefix(validSource, "package p"),
			}
			// A broken ordinary dependency excludes importers; broken internal
			// tests only invalidate variants that actually share their files.
			root := projectFixture(t, files)
			var paths []string
			before := make(map[string]os.FileInfo)
			for name := range files {
				if filepath.Ext(name) == ".go" {
					path := filepath.Join(root, name)
					paths = append(paths, path)
					before[path], _ = os.Stat(path)
				}
			}
			report, sources, err := rewriteProject(context.Background(), NewSourceConfig(WithDryRun(true)), paths, nil)
			want := 1
			if bad == "bad/bad_test.go" {
				want = 2
			}
			if err != nil || report.ProposedFilesModified != want || len(report.Diagnostics) == 0 {
				t.Fatalf("partial preview failed: %+v, %v", report, err)
			}
			for _, name := range []string{"bad/good.go", bad} {
				sr := sources[filepath.Join(root, name)]
				if !sr.Skipped || sr.After != sr.Before || sr.TotalErrorsEnhanced != 0 {
					t.Fatalf("broken variant exposed proposal: %+v", sr)
				}
			}
			if bad == "bad/bad.go" && !sources[filepath.Join(root, "dependent/p.go")].Skipped {
				t.Fatal("broken dependency's importer not skipped")
			}
			for path, info := range before {
				after, _ := os.Stat(path)
				if !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || readProjectFile(t, path) != sources[path].Before {
					t.Fatal("preview changed source: " + path)
				}
			}
			// Explicit invalid selection aborts even with a valid selected sibling.
			report, sources, err = rewriteProject(context.Background(), NewSourceConfig(), paths, map[string]bool{filepath.Join(root, "bad/good.go"): true})
			if err == nil || report.TotalFilesModified != 0 || len(report.Changes) != 0 {
				t.Fatalf("explicit invalid target accepted: %+v, %v", report, err)
			}
			for path, sr := range sources {
				if sr.Before != sr.After || readProjectFile(t, path) != sr.Before {
					t.Fatal("failure exposed or wrote proposal: " + path)
				}
			}
		})
	}
}

func TestProjectLoadingWorkspace(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"go.work":       "go 1.22.5\nuse (\n ./a\n ./b\n)\n",
		"a/go.mod":      "module example.com/a\n\ngo 1.22.5\n",
		"a/a.go":        validSource + "func Source() error { return source() }\n",
		"b/go.mod":      "module example.com/b\n\ngo 1.22.5\n",
		"b/b.go":        "package b\nimport p \"example.com/a\"\nfunc F() error { e := p.Source(); if e != nil { return e }; return nil }\n",
		"b/bad/bad.go":  "package bad; var _ = missing\n",
		"tools/go.mod":  "module example.com/tools\n\ngo 1.22.5\n",
		"tools/tool.go": "not valid Go",
	})
	// Empty GOWORK asks Go to discover the workspace rather than pinning it.
	t.Setenv("GOWORK", "")
	for _, preview := range []bool{true, false} {
		report, err := LocalDirectory(projectDirectory(root), NewSourceConfig(WithDryRun(preview)))
		if err != nil || (preview && report.ProposedFilesModified != 2) || (!preview && report.TotalFilesModified != 2) {
			t.Fatalf("workspace partial load failed: %+v, %v", report, err)
		}
		found := false
		for _, d := range report.Diagnostics {
			found = found || strings.Contains(d.Message, "not listed in active workspace")
		}
		if !found || readProjectFile(t, filepath.Join(root, "tools/tool.go")) != "not valid Go" {
			t.Fatal("unlisted tools module not clearly skipped")
		}
	}
	if _, err := LocalFile(NewSourceConfig(), filepath.Join(root, "tools/tool.go")); err == nil {
		t.Fatal("explicit unlisted workspace module accepted")
	}
}

func TestProjectLoadingBuildTags(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go":      "//go:build selected\n\n" + validSource,
		"broken.go": "//go:build !selected\n\npackage p; var _ = missing\n",
	})
	t.Setenv("GOFLAGS", "-tags=selected -mod=readonly")
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig(WithDryRun(true)))
	if err != nil || report.ProposedFilesModified != 1 || len(report.Changes) != 1 || report.Changes[0].Path != filepath.Join(root, "p.go") {
		t.Fatalf("active build tags lost: %+v, %v", report, err)
	}
}

func TestProjectLoadingAllInvalid(t *testing.T) {
	for _, preview := range []bool{false, true} {
		root := projectFixture(t, map[string]string{"p.go": validSource, "p_test.go": "package p; var _ = missing\n"})
		path := filepath.Join(root, "p.go")
		info, _ := os.Stat(path)
		report, sources, err := rewriteProject(context.Background(), NewSourceConfig(WithDryRun(preview)), []string{path}, nil)
		if err == nil || report.TotalFilesModified != 0 || report.ProposedFilesModified != 0 || len(report.Changes) != 0 || len(report.Diagnostics) == 0 {
			t.Fatalf("all-invalid selection accepted: %+v, %v", report, err)
		}
		after, _ := os.Stat(path)
		if sources[path].After != validSource || readProjectFile(t, path) != validSource || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
			t.Fatal("failed validation exposed proposal or changed source")
		}
	}
}

func TestProjectLoadingOverlayBaseline(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"good/p.go":      "package good\nfunc F() error { return nil }\n",
		"dependent/p.go": "package dependent\nimport \"example.com/project/good\"\nfunc F() error { return good.F() }\n",
		"broken/p.go":    "package broken; var _ = missing\n",
	})
	cfg, err := projectLoadConfig(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatal(err)
	}
	baseline := make(map[string]bool)
	for _, pkg := range pkgs {
		if projectPackageValid(pkg) {
			baseline[pkg.ID] = true
		}
	}
	if len(baseline) != 2 {
		t.Fatalf("unexpected valid roots: %v", baseline)
	}
	if err := validateProjectRoots(pkgs, baseline, &DirectoryReport{}); err != nil {
		t.Fatalf("baseline-broken package aborted validation: %v", err)
	}
	path := filepath.Join(root, "good/p.go")
	before := readProjectFile(t, path)
	info, _ := os.Stat(path)
	// The edited package still typechecks; only its originally valid importer
	// fails. Baseline failures must not hide this new downstream failure.
	cfg.Overlay = map[string][]byte{path: []byte("package good\nfunc F() int { return 0 }\n")}
	pkgs, err = packages.Load(cfg, "./...")
	if err != nil {
		t.Fatal(err)
	}
	var report DirectoryReport
	if err := validateProjectRoots(pkgs, baseline, &report); err == nil || !strings.Contains(err.Error(), "example.com/project/dependent") {
		t.Fatalf("newly invalid dependent accepted: %+v, %v", report, err)
	}
	if err := validateProjectRoots(nil, baseline, &DirectoryReport{}); err == nil {
		t.Fatal("missing baseline roots accepted")
	}
	after, _ := os.Stat(path)
	if readProjectFile(t, path) != before || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
		t.Fatal("overlay validation wrote source")
	}
}
