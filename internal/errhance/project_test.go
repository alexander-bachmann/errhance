package errhance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexander-bachmann/errhance/internal/fastwalk"
	"golang.org/x/tools/go/packages"
)

func projectFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/project\n\ngo 1.22.5\n"
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0640); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func projectDirectory(root string, opts ...fastwalk.Option) DirectoryConfig {
	defaults := []fastwalk.Option{fastwalk.WithRootFilepath(root), fastwalk.WithOnlyExtensions([]string{"go"}), fastwalk.WithSkipDirectories([]string{"vendor"})}
	return NewDirectoryConfig(fastwalk.NewConfig(nil, append(defaults, opts...)...))
}

func readProjectFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProjectPackagesAndTests(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go": `package p; import "errors"; func Source() error { return errors.New("failure") }`,
		"f.go": "package p\nfunc F() error { e := Source(); if e != nil { return e }; return nil }",
		"p_test.go": `package p
import "testing"; func TestF(t *testing.T) { _ = F() }; func helper() error { e := Source(); if e != nil { return e }; return nil }`,
		"external_test.go": `package p_test
import "example.com/project"; func helper() error { e := p.Source(); if e != nil { return e }; return nil }`,
		"sub/sub.go": `package sub
import "example.com/project"; func F() error { e := p.Source(); if e != nil { return e }; return nil }`,
	})
	before := make(map[string]os.FileMode)
	for _, name := range []string{"f.go", "p_test.go", "external_test.go", "sub/sub.go"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = info.Mode().Perm()
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 4 || report.TotalErrorsEnhanced != 4 {
		t.Fatalf("report=%+v, error=%v", report, err)
	}
	for _, name := range []string{"f.go", "p_test.go", "external_test.go", "sub/sub.go"} {
		path := filepath.Join(root, name)
		data := readProjectFile(t, path)
		if !strings.Contains(data, `"fmt"`) || !strings.Contains(data, "Errorf(") {
			t.Errorf("imports/rewrite missing in %s: %s", name, data)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != before[name] {
			t.Fatalf("permissions changed: %v, %v", info, err)
		}
	}
	pkgs, err := packages.Load(projectConfig(context.Background(), root), "./...")
	if err != nil {
		t.Fatal(err)
	}
	if err := projectErrors(pkgs, &DirectoryReport{}); err != nil {
		t.Fatal(err)
	}
	again, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || again.TotalFilesModified != 0 || again.TotalErrorsEnhanced != 0 {
		t.Fatalf("not idempotent: %+v, %v", again, err)
	}
}

func TestProjectRuntimeContractsAndClosures(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go": `package p
import ("errors"; "io")
var stop = errors.New("stop")
func source() error { return stop }
func enhanced() error { err := source(); if err != nil { return err }; return nil }
func identity() error { err := source(); if err != nil { return err }; return nil }
func forward() error { return identity() }
func previous() error { return stop }
func some(fn func() error) error { return fn() }
func closure() error {
 err := previous()
 err = some(func() error { if err != nil { return err }; return nil })
 return err
}
type reader struct{}
func eof() error { return io.EOF }
func eofHelper() error { err := eof(); if err != nil { return err }; return nil }
func (reader) Read([]byte) (int, error) { return 0, eofHelper() }
`,
		"p_test.go": `package p
import ("errors"; "io"; "strings"; "testing")
func TestBehavior(t *testing.T) {
 if err := enhanced(); !errors.Is(err, stop) || !strings.HasPrefix(err.Error(), "source: ") { t.Fatalf("missing context or cause: %v", err) }
 copy := forward()
 if copy != stop { t.Fatalf("identity lost: %v", copy) }
 if err := closure(); !errors.Is(err, stop) || err.Error() != "stop" { t.Fatalf("closure provenance changed: %v", err) }
 if _, err := io.ReadAll(reader{}); err != nil { t.Fatalf("EOF contract changed: %v", err) }
}
`,
	})
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("unexpected rewrites: %+v, %v", report, err)
	}
	cmd := exec.Command("go", "test", "-count=1", "./...")
	cmd.Dir = root
	cmd.Env = projectConfig(context.Background(), root).Env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("transformed program behavior: %v\n%s", err, output)
	}
}

func TestLocalFilePackageAwareSingleTarget(t *testing.T) {
	const target = "package p\nfunc f() error { e := source(); if e != nil { return e }; return nil }"
	const sibling = `package p; import "errors"; func source() error { return errors.New("failure") }; func g() error { e := source(); if e != nil { return e }; return nil }`
	root := projectFixture(t, map[string]string{"f.go": target, "sibling.go": sibling})
	path := filepath.Join(root, "f.go")
	report, err := LocalFile(NewSourceConfig(), path)
	if err != nil || report.TotalErrorsEnhanced != 1 || report.Before != target || report.After != readProjectFile(t, path) {
		t.Fatalf("report=%+v, error=%v", report, err)
	}
	if got := readProjectFile(t, filepath.Join(root, "sibling.go")); got != sibling {
		t.Fatal("unselected sibling changed")
	}
	before, _ := os.Stat(path)
	report, err = LocalFile(NewSourceConfig(), path)
	after, _ := os.Stat(path)
	if !errors.Is(err, ErrNoChanges) || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("no-op touched file: %+v, %v", report, err)
	}
}

func TestProjectPreflightNoWrites(t *testing.T) {
	for _, bad := range []string{"bad.go", "bad_test.go", "sub/bad.go"} {
		t.Run(bad, func(t *testing.T) {
			root := projectFixture(t, map[string]string{"good.go": validSource, bad: "package p; var _ = missing\n"})
			path := filepath.Join(root, "good.go")
			before, _ := os.Stat(path)
			report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
			after, _ := os.Stat(path)
			if bad == "sub/bad.go" {
				if err != nil || report.TotalFilesModified != 1 || len(report.Diagnostics) == 0 {
					t.Fatalf("independent good package not processed: %+v, %v", report, err)
				}
				if readProjectFile(t, filepath.Join(root, bad)) != "package p; var _ = missing\n" {
					t.Fatal("invalid package changed")
				}
				return
			}
			if err == nil || report.TotalFilesModified != 0 || len(report.Diagnostics) == 0 {
				t.Fatalf("bad package accepted: %+v, %v", report, err)
			}
			if readProjectFile(t, path) != validSource || !os.SameFile(before, after) {
				t.Fatal("preflight failure wrote source")
			}
		})
	}
}

func TestLocalFileChecksUnselectedTests(t *testing.T) {
	root := projectFixture(t, map[string]string{"good.go": validSource, "bad_test.go": "package p; var _ = missing\n"})
	path := filepath.Join(root, "good.go")
	if _, err := LocalFile(NewSourceConfig(), path); err == nil {
		t.Fatal("invalid unselected test accepted")
	}
	if readProjectFile(t, path) != validSource {
		t.Fatal("source changed")
	}
}

func TestProjectTestVariantAlias(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go": validSource, "p_test.go": "package p\nvar errhancefmt = 1\n",
	})
	path := filepath.Join(root, "p.go")
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("test variant alias collision: %+v, %v", report, err)
	}
	if !strings.Contains(readProjectFile(t, path), `errhancefmt2 "fmt"`) {
		t.Fatal("test variant name was not considered")
	}
}

func TestProjectExcludedOnlyPackage(t *testing.T) {
	src := "//go:build errhance_never_enabled\n\n" + validSource
	root := projectFixture(t, map[string]string{"excluded.go": src})
	report, err := LocalFile(NewSourceConfig(), filepath.Join(root, "excluded.go"))
	if err == nil || errors.Is(err, ErrNoChanges) || len(report.Diagnostics) == 0 || !strings.Contains(report.Diagnostics[0].Message, "excluded") {
		t.Fatalf("missing excluded-file diagnostic: %+v, %v", report, err)
	}
	directory, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || directory.TotalFilesModified != 0 || len(directory.Diagnostics) != 1 {
		t.Fatalf("excluded directory should be skipped: %+v, %v", directory, err)
	}
}

func TestProjectVendoredDependency(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go":                          "package p\nimport \"example.com/dep\"\nfunc f() error { e := dep.F(); if e != nil { return e }; return nil }\n",
		"vendor/modules.txt":            "# example.com/dep v1.0.0\n## explicit; go 1.22.5\nexample.com/dep\n",
		"vendor/example.com/dep/dep.go": "package dep\nfunc F() error { return nil }\n",
	})
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.22.5\nrequire example.com/dep v1.0.0\n"), 0640); err != nil {
		t.Fatal(err)
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 {
		t.Fatalf("vendored dependency failed: %+v, %v", report, err)
	}
}

func TestProjectSelectionAndExcludedBuild(t *testing.T) {
	const excluded = "//go:build errhance_never_enabled\n\n" + validSource
	root := projectFixture(t, map[string]string{
		"good.go": validSource, "excluded.go": excluded,
		"vendor/ignored/invalid.go": "not Go", ".hidden/invalid.go": "not Go", "notes.txt": "not Go",
	})
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 || len(report.Diagnostics) == 0 {
		t.Fatalf("selection failed: %+v, %v", report, err)
	}
	path := filepath.Join(root, "excluded.go")
	sr, err := LocalFile(NewSourceConfig(), path)
	if err == nil || errors.Is(err, ErrNoChanges) || len(sr.Diagnostics) == 0 || !sr.Skipped {
		t.Fatalf("excluded explicit file accepted: %+v, %v", sr, err)
	}
	report, err = LocalDirectory(projectDirectory(root, fastwalk.WithOnlyFilepaths([]string{path})), NewSourceConfig())
	if err == nil || len(report.Diagnostics) == 0 || report.TotalFilesModified != 0 {
		t.Fatalf("excluded explicit directory selection accepted: %+v, %v", report, err)
	}
	if readProjectFile(t, path) != excluded {
		t.Fatal("excluded source changed")
	}
}

func TestProjectExplicitSelectionHonorsFilters(t *testing.T) {
	root := projectFixture(t, map[string]string{"good.go": validSource, "other/other.go": validSource, "vendor/ignored/bad.go": "invalid"})
	path := filepath.Join(root, "other/other.go")
	report, err := LocalDirectory(projectDirectory(root, fastwalk.WithOnlyFilepaths([]string{path, filepath.Join(root, "vendor/ignored/bad.go")})), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 {
		t.Fatalf("explicit selection failed: %+v, %v", report, err)
	}
	if readProjectFile(t, filepath.Join(root, "good.go")) != validSource {
		t.Fatal("unselected package changed")
	}
}

func TestProjectMissingExplicitTarget(t *testing.T) {
	root := projectFixture(t, map[string]string{"good.go": validSource})
	missing := filepath.Join(root, "missing.go")
	report, err := LocalDirectory(projectDirectory(root, fastwalk.WithOnlyFilepaths([]string{missing})), NewSourceConfig())
	if err == nil || report.TotalFilesModified != 0 {
		t.Fatalf("missing explicit target accepted: %+v, %v", report, err)
	}
	if readProjectFile(t, filepath.Join(root, "good.go")) != validSource {
		t.Fatal("missing-target failure changed another file")
	}
}

func TestProjectNestedModuleAndCancellation(t *testing.T) {
	root := projectFixture(t, map[string]string{"good.go": validSource, "nested/go.mod": "module example.com/nested\n\ngo 1.22.5\n", "nested/good.go": validSource})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if report, err := LocalDirectoryContext(ctx, projectDirectory(root), NewSourceConfig()); !errors.Is(err, context.Canceled) || report.TotalFilesModified != 0 {
		t.Fatalf("cancellation failed: %+v, %v", report, err)
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 2 {
		t.Fatalf("nested module failed: %+v, %v", report, err)
	}
}

func TestProjectMissingDependencyDoesNotEditModule(t *testing.T) {
	t.Setenv("GOPRIVATE", "unavailable.example")
	root := projectFixture(t, map[string]string{"p.go": `package p; import "unavailable.example/dep"; var _ = dep.F`})
	mod := filepath.Join(root, "go.mod")
	before := readProjectFile(t, mod)
	if _, err := LocalFile(NewSourceConfig(), filepath.Join(root, "p.go")); err == nil {
		t.Fatal("missing dependency accepted")
	}
	if readProjectFile(t, mod) != before {
		t.Fatal("go.mod changed")
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("unexpected go.sum: %v", err)
	}
}

func TestLocalFileTestOnlyDefinitions(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go":           "package p\n",
		"source_test.go": "package p\nfunc source() error { return nil }\n",
		"target_test.go": "package p\nfunc f() error { e := source(); if e != nil { return e }; return nil }\n",
	})
	report, err := LocalFile(NewSourceConfig(), filepath.Join(root, "target_test.go"))
	if err != nil || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("test-only definitions not resolved: %+v, %v", report, err)
	}
}

func TestProjectGeneratedFiles(t *testing.T) {
	src := "// Code generated by test. DO NOT EDIT.\n" + validSource
	root := projectFixture(t, map[string]string{"generated.go": src})
	path := filepath.Join(root, "generated.go")
	report, err := LocalFile(NewSourceConfig(), path)
	if !errors.Is(err, ErrNoChanges) || !report.Skipped || readProjectFile(t, path) != src {
		t.Fatalf("generated source touched: %+v, %v", report, err)
	}
	report, err = LocalFile(NewSourceConfig(WithGenerated(true)), path)
	if err != nil || report.Skipped || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("generated opt-in failed: %+v, %v", report, err)
	}
}

func TestProjectUnsafePaths(t *testing.T) {
	root := projectFixture(t, map[string]string{"p.go": validSource})
	path := filepath.Join(root, "p.go")
	for _, kind := range []string{"symlink", "ancestor", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			alias := filepath.Join(root, kind+".go")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(path, alias)
			case "ancestor":
				alias = filepath.Join(root, "linked")
				err = os.Symlink(root, alias)
			case "hardlink":
				err = os.Link(path, alias)
			}
			if err != nil {
				t.Skip(err)
			}
			defer os.Remove(alias)
			target := alias
			if kind == "ancestor" {
				target = filepath.Join(alias, "p.go")
			}
			if _, err := LocalFile(NewSourceConfig(), target); err == nil {
				t.Fatal("unsafe target accepted")
			}
			if readProjectFile(t, path) != validSource {
				t.Fatal("unsafe target changed")
			}
		})
	}
}

func TestAtomicReplace(t *testing.T) {
	root := projectFixture(t, map[string]string{"p.go": validSource})
	path := filepath.Join(root, "p.go")
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	original := projectSnapshot{data: []byte(validSource), info: info}
	if committed, err := atomicReplace(context.Background(), path, original, []byte("package p\n")); err != nil || !committed {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if os.SameFile(info, after) || after.Mode() != info.Mode() || readProjectFile(t, path) != "package p\n" {
		t.Fatal("replacement was not atomic or lost permissions")
	}
	if committed, err := atomicReplace(context.Background(), path, original, []byte("overwrite concurrent change")); err == nil || committed {
		t.Fatal("stale snapshot accepted")
	}
	if readProjectFile(t, path) != "package p\n" {
		t.Fatal("stale snapshot changed file")
	}
	matches, err := filepath.Glob(filepath.Join(root, ".errhance-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files leaked: %v, %v", matches, err)
	}
}

func TestAtomicReplaceWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Chmod does not control directory write access on Windows")
	}
	root := projectFixture(t, map[string]string{"p.go": validSource})
	path := filepath.Join(root, "p.go")
	info, err := safeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0700)
	probe, err := os.CreateTemp(root, "probe")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("current user bypasses directory write permissions")
	}
	if committed, err := atomicReplace(context.Background(), path, projectSnapshot{data: []byte(validSource), info: info}, []byte("package p\n")); err == nil || committed {
		t.Fatal("write unexpectedly succeeded")
	}
	if readProjectFile(t, path) != validSource {
		t.Fatal("failed write damaged original")
	}
}

func TestProjectActiveCgo(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("C compiler unavailable")
	}
	root := projectFixture(t, map[string]string{
		"cgo.go":   "package p\n/* int value(void) { return 1; } */\nimport \"C\"\nfunc cvalue() int { return int(C.value()) }\n",
		"plain.go": validSource,
	})
	t.Setenv("CGO_ENABLED", "1")
	path := filepath.Join(root, "cgo.go")
	before := readProjectFile(t, path)
	source, err := LocalFile(NewSourceConfig(), path)
	if err == nil || errors.Is(err, ErrNoChanges) || !source.Skipped || len(source.Diagnostics) == 0 || !strings.Contains(source.Diagnostics[0].Message, "cgo source mapping") {
		t.Fatalf("cgo file misdiagnosed: %+v, %v", source, err)
	}
	explicit := projectDirectory(root, fastwalk.WithOnlyFilepaths([]string{path, filepath.Join(root, "plain.go")}))
	if report, err := LocalDirectory(explicit, NewSourceConfig()); err == nil || report.TotalFilesModified != 0 {
		t.Fatalf("unsupported explicit target did not fail closed: %+v, %v", report, err)
	}
	if readProjectFile(t, filepath.Join(root, "plain.go")) != validSource {
		t.Fatal("explicit cgo selection changed a sibling")
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 {
		t.Fatalf("cgo sibling rewrite failed: %+v, %v", report, err)
	}
	if readProjectFile(t, path) != before {
		t.Fatal("cgo original changed")
	}
}

func TestProjectModuleModes(t *testing.T) {
	root := projectFixture(t, map[string]string{"p.go": validSource, "vendor/modules.txt": ""})
	for _, flags := range []string{"-mod=readonly", "'-mod=vendor'", "-mod=mod"} {
		t.Run(flags, func(t *testing.T) {
			t.Setenv("GOFLAGS", flags)
			cfg, err := projectLoadConfig(context.Background(), root)
			if flags == "-mod=mod" {
				if err == nil {
					t.Fatal("mutating module mode allowed")
				}
				return
			}
			if err != nil || len(cfg.BuildFlags) != 1 || cfg.BuildFlags[0] != strings.Trim(flags, "'") {
				t.Fatalf("GOFLAGS mode ignored: %+v, %v", cfg, err)
			}
		})
	}
	// An explicit readonly mode must not be silently replaced with vendor mode.
	t.Setenv("GOFLAGS", "-mod=readonly")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.22.5\nrequire example.com/unavailable v1.0.0\n"), 0640); err != nil {
		t.Fatal(err)
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 {
		t.Fatalf("readonly mode not respected: %+v, %v", report, err)
	}
}

func TestProjectWorkspaceVendor(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"go.work":                       "go 1.22.5\nuse ./app\n",
		"app/go.mod":                    "module example.com/app\n\ngo 1.22.5\nrequire example.com/dep v1.0.0\n",
		"app/p.go":                      "package p\nimport \"example.com/dep\"\nfunc f() error { e := dep.F(); if e != nil { return e }; return nil }\n",
		"vendor/modules.txt":            "## workspace\n# example.com/dep v1.0.0\n## explicit; go 1.22.5\nexample.com/dep\n",
		"vendor/example.com/dep/dep.go": "package dep\nfunc F() error { return nil }\n",
	})
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))
	report, err := LocalDirectory(projectDirectory(filepath.Join(root, "app")), NewSourceConfig())
	if err != nil || report.TotalFilesModified != 1 {
		t.Fatalf("workspace vendor missed: %+v, %v", report, err)
	}
}

// checkContext makes cancellation checkpoints deterministic without production
// hooks or races. It delegates cancellation state and Done to a real context.
type checkContext struct {
	context.Context
	check func()
}

func (ctx checkContext) Err() error { ctx.check(); return ctx.Context.Err() }

func TestAtomicReplaceCancellation(t *testing.T) {
	for _, point := range []string{"before staging", "before rename", "after rename"} {
		t.Run(point, func(t *testing.T) {
			root := projectFixture(t, map[string]string{"p.go": validSource})
			path := filepath.Join(root, "p.go")
			info, err := safeFile(path)
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := checkContext{Context: base, check: func() {
				switch point {
				case "before staging":
					cancel()
				case "before rename":
					files, _ := filepath.Glob(filepath.Join(root, ".errhance-*"))
					if len(files) != 0 {
						cancel()
					}
				case "after rename":
					current, err := os.Stat(path)
					if err == nil && !os.SameFile(info, current) {
						cancel()
					}
				}
			}}
			committed, err := atomicReplace(ctx, path, projectSnapshot{data: []byte(validSource), info: info}, []byte("package p\n"))
			if !errors.Is(err, context.Canceled) || committed != (point == "after rename") {
				t.Fatalf("committed=%v, error=%v", committed, err)
			}
			want := validSource
			if committed {
				want = "package p\n"
			}
			if readProjectFile(t, path) != want {
				t.Fatal("unexpected file contents")
			}
			files, _ := filepath.Glob(filepath.Join(root, ".errhance-*"))
			if len(files) != 0 {
				t.Fatalf("temporary files leaked: %v", files)
			}
		})
	}
}

func TestProjectCancellationRetainsCommittedCounts(t *testing.T) {
	root := projectFixture(t, map[string]string{"a/a.go": validSource, "b/b.go": validSource})
	path := filepath.Join(root, "a/a.go")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := checkContext{Context: base, check: func() {
		current, err := os.Stat(path)
		if err == nil && !os.SameFile(info, current) {
			cancel()
		}
	}}
	report, err := LocalDirectoryContext(ctx, projectDirectory(root), NewSourceConfig())
	if !errors.Is(err, context.Canceled) || report.TotalFilesModified != 1 || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("committed counts lost: %+v, %v", report, err)
	}
	if readProjectFile(t, filepath.Join(root, "b/b.go")) != validSource {
		t.Fatal("continued writes after cancellation")
	}
}
