package errhance

import (
	"context"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// Build the same graph shape as LoadSyntax without running a subprocess. In
// particular, variants may share PkgPath while having distinct IDs and Types.
func validationPackage(t *testing.T, id, path string, sources map[string]string, deps map[string]*packages.Package) *packages.Package {
	t.Helper()
	pkg := &packages.Package{ID: id, PkgPath: path, Fset: token.NewFileSet(), Imports: deps, TypesSizes: types.SizesFor("gc", "amd64")}
	for name, source := range sources {
		name = filepath.Join(t.TempDir(), name)
		file, err := parser.ParseFile(pkg.Fset, name, source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		pkg.Syntax = append(pkg.Syntax, file)
		pkg.GoFiles = append(pkg.GoFiles, name)
		pkg.CompiledGoFiles = append(pkg.CompiledGoFiles, name)
	}
	cfg := types.Config{GoVersion: "go1.22", Sizes: pkg.TypesSizes, Importer: rewriteImporter(func(path string) (*types.Package, error) {
		if dep := deps[path]; dep != nil {
			return dep.Types, nil
		}
		return importer.Default().Import(path)
	})}
	var err error
	pkg.Types, err = cfg.Check(path, pkg.Fset, pkg.Syntax, nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Name = pkg.Types.Name()
	return pkg
}

func TestValidateRewritesBodies(t *testing.T) {
	const source = `package p
type E struct{}
func (*E) Error() string { return "" }
var V = func() *E { return nil }
func F() (result error) { type local int; var _ local; return nil }
`
	pkg := validationPackage(t, "p", "p", map[string]string{"p.go": source, "other.go": "package p; var _ error = V()"}, nil)
	var path string
	var original *ast.File
	for i, name := range pkg.GoFiles {
		if filepath.Base(name) == "p.go" {
			path, original = name, pkg.Syntax[i]
		}
	}
	position := pkg.Fset.Position(original.Pos())
	fingerprint := rewriteFingerprint(original)
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"body", strings.Replace(source, "var _ local", "var x local; _ = x", 1), true},
		{"literal body", strings.Replace(source, "return nil }", "return new(E) }", 1), true},
		{"fresh fmt", strings.Replace(strings.Replace(source, "package p", "package p\nimport ef \"fmt\"", 1), "var _ local; return nil", "var _ local; return ef.Errorf(\"bad: %w\", result)", 1), true},
		{"invalid body", strings.Replace(source, "var _ local", "var _ local = missing", 1), false},
		{"signature", strings.Replace(source, "result error", "result *E", 1), false},
		{"package", strings.Replace(source, "package p", "package q", 1), false},
		{"initializer type", strings.Replace(source, "func() *E", "func() error", 1), false},
		{"defined to alias", strings.Replace(source, "type E struct{}", "type E = struct{}", 1), false},
		{"new import", strings.Replace(source, "package p", "package p\nimport _ \"strings\"", 1), false},
		{"fmt foreign type", strings.Replace(strings.Replace(source, "package p", "package p\nimport \"fmt\"", 1), "var _ local", "var _ fmt.Stringer", 1), false},
		{"build constraint", "//go:build linux\n\n" + source, false},
		{"comments", strings.Replace(source, "type E", "// ordinary comment\ntype E", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var report DirectoryReport
			err := validateRewrites(context.Background(), []*packages.Package{pkg}, map[string]bool{"p": true}, map[string][]byte{path: []byte(tc.source)}, &report)
			if (err == nil) != tc.valid || errors.Is(err, errRewriteValidationUnsupported) {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if tc.name == "invalid body" && (len(report.Diagnostics) == 0 || report.Diagnostics[0].Position.Filename != path) {
				t.Fatalf("missing positioned type error: %+v", report)
			}
			if pkg.TypesInfo != nil || pkg.Fset.Position(original.Pos()) != position || rewriteFingerprint(original) != fingerprint {
				t.Fatal("validation modified original AST, positions, or Info")
			}
		})
	}
}

func TestValidateRewritesLiteralFormatting(t *testing.T) {
	const source = `package p; var N = 1E3; const Hex = 0XFF; func F() error { return nil }`
	pkg := validationPackage(t, "p", "p", map[string]string{"p.go": source}, nil)
	for _, tc := range []struct {
		name, after string
		valid       bool
	}{
		{"formatting", `package p; var N = 1e3; const Hex = 0xff; func F() error { return nil }`, true},
		{"different value", `package p; var N = 1e4; const Hex = 0xff; func F() error { return nil }`, false},
		{"different inferred type", `package p; var N = 1000; const Hex = 0xff; func F() error { return nil }`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRewrites(context.Background(), []*packages.Package{pkg}, map[string]bool{"p": true}, map[string][]byte{pkg.GoFiles[0]: []byte(tc.after)}, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestValidateRewritesBuildFormatting(t *testing.T) {
	const body = "package p\nfunc F() error { return nil }\n"
	source := "// +build linux,amd64 darwin\n\n" + body
	pkg := validationPackage(t, "p", "p", map[string]string{"p.go": source}, nil)
	for _, tc := range []struct {
		name, header string
		valid        bool
	}{
		{"modern equivalent", "//go:build (linux && amd64) || darwin\n// +build linux,amd64 darwin\n\n", true},
		{"changed constraint", "//go:build windows\n\n", false},
		{"removed constraint", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRewrites(context.Background(), []*packages.Package{pkg}, map[string]bool{"p": true}, map[string][]byte{pkg.GoFiles[0]: []byte(tc.header + body)}, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestValidateRewritesPreservesVariadicInitializer(t *testing.T) {
	const source = "package p; func first[T any](xs ...T) T { return xs[0] }; var V = first([]int{1}...)"
	pkg := validationPackage(t, "p", "p", map[string]string{"p.go": source}, nil)
	consumer := validationPackage(t, "q", "q", map[string]string{"q.go": `package q; import "p"; var _ int = p.V`}, map[string]*packages.Package{"p": pkg})
	overlay := map[string][]byte{pkg.GoFiles[0]: []byte(strings.Replace(source, "{1}...", "{1}", 1))}
	if err := validateRewrites(context.Background(), []*packages.Package{pkg, consumer}, map[string]bool{"p": true, "q": true}, overlay, nil); err == nil {
		t.Fatal("variadic initializer changed exported inferred type")
	}
}

func TestValidateRewritesIdentityAndVariants(t *testing.T) {
	dep := validationPackage(t, "dep", "dep", map[string]string{"dep.go": "package dep; type T struct{}; func F() T { return T{} }"}, nil)
	middle := validationPackage(t, "middle", "middle", map[string]string{"middle.go": "package middle; import \"dep\"; func F() dep.T { return dep.F() }"}, map[string]*packages.Package{"dep": dep})
	const source = "package p; import (\"dep\"; \"middle\"); func F() dep.T { return middle.F() }"
	pkg := validationPackage(t, "p", "p", map[string]string{"p.go": source}, map[string]*packages.Package{"dep": dep, "middle": middle})
	variant := validationPackage(t, "p [p.test]", "p", map[string]string{"p.go": source, "p_test.go": "package p; var OnlyTest = 1; var ef = 1; var _ = F()"}, pkg.Imports)
	// Match shared production filenames as packages.Load does.
	for i, file := range variant.Syntax {
		if filepath.Base(variant.GoFiles[i]) == "p.go" {
			file, _ = parser.ParseFile(variant.Fset, pkg.GoFiles[0], source, 0)
			variant.Syntax[i] = file
			variant.GoFiles[i], variant.CompiledGoFiles[i] = pkg.GoFiles[0], pkg.GoFiles[0]
		}
	}
	const externalSource = "package p_test; import \"p\"; var _ = p.OnlyTest; var _ = p.F(); func init() { _ = p.F() }"
	external := validationPackage(t, "p_test [p.test]", "p_test", map[string]string{"external_test.go": externalSource}, map[string]*packages.Package{"p": variant})
	roots := []*packages.Package{pkg, variant, external}
	baseline := map[string]bool{"p": true, variant.ID: true, external.ID: true}
	overlay := map[string][]byte{
		dep.GoFiles[0]:      []byte("package dep; type T struct{}; func F() T { var v T; return v }"),
		pkg.GoFiles[0]:      []byte(strings.Replace(source, "return middle.F()", "var v dep.T = middle.F(); return v", 1)),
		external.GoFiles[0]: []byte(strings.Replace(externalSource, "_ = p.F() }", "_ = p.OnlyTest; _ = p.F() }", 1)),
	}
	if err := validateRewrites(context.Background(), roots, baseline, overlay, nil); err != nil {
		t.Fatal(err)
	}
	// This body works only in the augmented variant, not the ordinary package.
	overlay[pkg.GoFiles[0]] = []byte(strings.Replace(source, "return middle.F()", "_ = OnlyTest; return middle.F()", 1))
	if err := validateRewrites(context.Background(), roots, baseline, overlay, nil); err == nil {
		t.Fatal("ordinary package body was not checked")
	}
	// This import is valid in production but collides with an unedited test
	// declaration. Checking only the production package would miss the error.
	overlay[pkg.GoFiles[0]] = []byte(strings.Replace(strings.Replace(source, "import (", "import (ef \"fmt\";", 1), "return middle.F()", "_ = ef.Errorf(\"bad\"); return middle.F()", 1))
	if err := validateRewrites(context.Background(), roots, baseline, overlay, nil); err == nil {
		t.Fatal("unedited variant source was not checked")
	}
}

func TestValidateRewritesLoadedGraph(t *testing.T) {
	const source = "package p; func F() error { return nil }"
	root := projectFixture(t, map[string]string{
		"p.go":        source,
		"p_test.go":   "package p; var _ = F()",
		"x_test.go":   "package p_test; import p \"example.com/project\"; var _ = p.F()",
		"use/use.go":  "package use; import (p \"example.com/project\"; \"fmt\"); var _ error = p.F(); var _ fmt.Stringer",
		"broken/p.go": "package broken; var _ = missing",
	})
	pkgs, err := packages.Load(projectConfig(context.Background(), root), "./...")
	if err != nil {
		t.Fatal(err)
	}
	baseline := make(map[string]bool)
	for _, pkg := range pkgs {
		if projectPackageValid(pkg) {
			baseline[pkg.ID] = true
		}
	}
	if len(baseline) < 4 {
		t.Fatalf("missing test variants: %v", baseline)
	}
	// The validator must use loaded fmt and per-variant import maps, not invoke
	// go or reconstruct unrelated importer graphs. Broken roots stay tolerated.
	t.Setenv("PATH", t.TempDir())
	overlay := map[string][]byte{filepath.Join(root, "p.go"): []byte("package p; import f \"fmt\"; func F() error { return f.Errorf(\"bad\") }")}
	if err := validateRewrites(context.Background(), pkgs, baseline, overlay, &DirectoryReport{}); err != nil {
		t.Fatal(err)
	}
	overlay[filepath.Join(root, "p.go")] = []byte("package p; func F() int { return 0 }")
	if err := validateRewrites(context.Background(), pkgs, baseline, overlay, nil); err == nil || errors.Is(err, errRewriteValidationUnsupported) {
		t.Fatalf("signature change needs hard failure, not fallback: %v", err)
	}
}

func TestValidateRewritesUnsupported(t *testing.T) {
	for _, name := range []string{"sizes", "syntax", "version", "cgo mapping", "cgo import", "unknown overlay", "missing root", "cancel"} {
		t.Run(name, func(t *testing.T) {
			pkg := validationPackage(t, "p", "p", map[string]string{"p.go": "package p; func F() {}"}, nil)
			overlay := map[string][]byte{pkg.GoFiles[0]: []byte("package p; func F() { println(1) }")}
			baseline := map[string]bool{"p": true}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "sizes":
				pkg.TypesSizes = nil
			case "syntax":
				pkg.Syntax = nil
			case "version":
				pkg.Types = types.NewPackage("p", "p")
				pkg.Types.MarkComplete()
			case "cgo mapping":
				pkg.GoFiles = append(pkg.GoFiles, "/original/cgo.go")
			case "cgo import":
				pkg.Syntax[0].Imports = append(pkg.Syntax[0].Imports, &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: `"C"`}})
			case "unknown overlay":
				overlay["/unknown.go"] = []byte("package q")
			case "missing root":
				baseline["absent"] = true
			case "cancel":
				cancel()
			}
			err := validateRewrites(ctx, []*packages.Package{pkg}, baseline, overlay, nil)
			if err == nil {
				t.Fatal("unsupported validation accepted")
			}
			if name == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if name != "missing root" && !errors.Is(err, errRewriteValidationUnsupported) {
				t.Fatalf("missing fallback signal: %v", err)
			}
		})
	}
}

func TestRewriteInvariantImportsAndDeclarations(t *testing.T) {
	const source = "package p; import f \"fmt\"; const C = 1; type A = int; var V = 1; func F() error { return f.Errorf(\"bad\") }"
	parse := func(source string) *ast.File {
		file, err := parser.ParseFile(token.NewFileSet(), "p.go", source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	for _, tc := range []struct {
		old, new string
		valid    bool
	}{
		{`import f "fmt"`, "import ( f `fmt` )", true},
		{"C = 1", "C = 2", false},
		{"A = int", "A int", false},
		{"V = 1", "V = int64(1)", false},
		{`f "fmt"`, `g "fmt"`, false},
		{`f "fmt"`, `f "strings"`, false},
		{`return f.Errorf("bad")`, `return nil`, true},
	} {
		if err := rewriteInvariant(parse(source), parse(strings.Replace(source, tc.old, tc.new, 1))); (err == nil) != tc.valid {
			t.Errorf("%s -> %s: %v", tc.old, tc.new, err)
		}
	}
}

func TestValidateRewritesGoVersionAndSizes(t *testing.T) {
	pkg := validationPackage(t, "p", "p", map[string]string{"p.go": "package p; func F() {}"}, nil)
	pkg.TypesSizes = types.SizesFor("gc", "386")
	for _, body := range []string{"var x int = 1 << 40; _ = x", "_ = func() { for range 3 {} }"} {
		pkg.Module = &packages.Module{GoVersion: "1.21"}
		// An empty Types version exercises the module-version fallback.
		pkg.Types = types.NewPackage("p", "p")
		pkg.Types.MarkComplete()
		err := validateRewrites(context.Background(), []*packages.Package{pkg}, map[string]bool{"p": true}, map[string][]byte{pkg.GoFiles[0]: []byte("package p; func F() { " + body + " }")}, nil)
		if err == nil || errors.Is(err, errRewriteValidationUnsupported) {
			t.Fatalf("version/sizes not enforced: %v", err)
		}
	}
}
