package errhance

import (
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestRewriteTyped(t *testing.T) {
	const base = "package p\nimport \"errors\"\nfunc source() error { return errors.New(\"failure\") }\nfunc other() error { return errors.New(\"other\") }\n"
	tests := []struct {
		name, source, want, diagnostic string
		count                          int
		overwrite                      bool
	}{
		{name: "guard", source: `func f() error { problem := source(); if problem != nil { return problem }; return nil }`, want: `errhancefmt.Errorf("source: %w", problem)`, count: 1},
		{name: "nested guard", source: `func f(b bool) error { e := source(); if e != nil { if b { return e }; return e }; return nil }`, want: `Errorf("source: %w", e)`, count: 2},
		{name: "early nil return", source: `func f() error { e := source(); if nil == e { return nil }; return e }`, count: 1, want: `Errorf("source: %w", e)`},
		{name: "parallel assignment", source: `func f() error { a, b := source(), other(); a, b = b, a; if a != nil { return a }; if b != nil { return b }; return nil }`, count: 2, want: `Errorf("other: %w", a)`},
		{name: "object shadow", source: `func f() error { e := source(); { e := other(); if e != nil { return e } }; if e != nil { return e }; return nil }`, count: 2, want: `Errorf("source: %w", e)`},
		{name: "var tuple", source: `func pair() (int, error) { return 0, source() }; func f() error { var _, e = pair(); if e != nil { return e }; return nil }`, count: 1, want: `Errorf("pair: %w", e)`},
		{name: "generic", source: `func produce[T any]() error { return source() }; func f() error { e := produce[int](); if e != nil { return e }; return nil }`, count: 1, want: `Errorf("produce[int]: %w", e)`},
		{name: "mutation clears guard", source: `func f() error { e := source(); if e != nil { e = other(); return e }; return nil }`, diagnostic: "not proven non-nil"},
		{name: "recheck mutation", source: `func f() error { e := source(); if e != nil { e = other(); if e != nil { return e } }; return nil }`, count: 1, want: `Errorf("other: %w", e)`},
		{name: "branch merge", source: `func f(b bool) error { e := source(); if b { e = other() }; if e != nil { return e }; return nil }`, diagnostic: "unknown error origin"},
		{name: "same branch origin", source: `func f(b bool) error { var e error; if b { e = source() } else { e = source() }; if e != nil { return e }; return nil }`, count: 1, want: `Errorf("source: %w", e)`},
		{name: "captured outer", source: `func f() error { e := source(); g := func() { e = nil }; g(); if e != nil { return e }; return nil }`, diagnostic: "captured error"},
		{name: "RHS callback reads existing error", source: `func invoke(cb func()) error { cb(); return source() }; func f() error { err := source(); err = invoke(func() { _ = err }); if err != nil { return err }; return nil }`, count: 1, want: `Errorf("invoke: %w", err)`},
		{name: "guard callback mutation", source: `func f() error { err := source(); if err != nil { func() { err = nil }(); return err }; return nil }`, diagnostic: "captured error"},
		{name: "nested immediate callbacks", source: `func f() error { err := source(); if err != nil { func() { func() { err = nil }() }(); return err }; return nil }`, diagnostic: "captured error"},
		{name: "deferred named result", source: `func f() (err error) { defer func() { err = other() }(); err = source(); if err != nil { return err }; return nil }`, diagnostic: "named result: captured error"},
		{name: "deferred named result different local", source: `func f() (err error) { defer func() { if err != nil { err = other() } }(); e := source(); if e != nil { return e }; return nil }`, diagnostic: "named result: captured error"},
		{name: "address exposed named result", source: `func mutate(e *error) { *e = other() }; func f() (err error) { defer mutate(&err); e := source(); if e != nil { return e }; return nil }`, diagnostic: "named result: address-exposed"},
		{name: "closure no inherited origin", source: `func f() func() error { e := source(); return func() error { if e != nil { return e }; return nil } }`, diagnostic: "captured or non-local"},
		{name: "closure local origin", source: `func f() func() error { return func() error { e := source(); if e != nil { return e }; return nil } }`, count: 1, want: `Errorf("source: %w", e)`},
		{name: "address exposed", source: `func mutate(e *error) { *e = nil }; func f() error { e := source(); if e != nil { mutate(&e); return e }; return nil }`, diagnostic: "address-exposed"},
		{name: "parameter", source: `func f(e error) error { if e != nil { return e }; return nil }`, diagnostic: "unknown error origin"},
		{name: "fake nil", source: `func f() error { nil := source(); e := source(); if e != nil { return e }; return nil }`, diagnostic: "not proven non-nil"},
		{name: "shadowed sentinel nil", source: `var sentinel = errors.New("sentinel"); func f() error { nil := sentinel; err := source(); if err != nil { return err }; return nil }`, diagnostic: "not proven non-nil"},
		{name: "not nil check", source: `func f() error { e := source(); if e != other() { return e }; return nil }`, diagnostic: "not proven non-nil"},
		{name: "concrete return", source: `type concrete struct{}; func (*concrete) Error() string { return "error" }; func makeError() *concrete { return &concrete{} }; func f() *concrete { e := makeError(); if e != nil { return e }; return nil }`, diagnostic: "concrete or non-builtin"},
		{name: "concrete to interface", source: `type concrete struct{}; func (*concrete) Error() string { return "error" }; func makeError() *concrete { return nil }; func f() error { e := makeError(); if e != nil { return e }; return nil }`, diagnostic: "concrete or non-builtin"},
		{name: "lookalike error", source: `type error = int; func f() error { err := 1; if err != 0 { return err }; return 0 }`},
		{name: "noeh", source: "func f() error { e := source(); if e != nil { // noeh\n return e }; return nil }"},
		{name: "noeh sibling", source: "func f() error { e := source(); if e != nil { // noeh\n return e }; e = other();\n if e != nil { return e }; return nil }", count: 1, want: `Errorf("other: %w", e)`},
		{name: "noeh string ignored", source: `func f() error { _ = "// noeh"; e := source(); if e != nil { return e }; return nil }`, count: 1, want: `Errorf("source: %w", e)`},
		{name: "loop invalidation", source: `func f(b bool) error { e := source(); for b { e = other(); if e != nil { return e } }; if e != nil { return e }; return nil }`, count: 1, want: `Errorf("other: %w", e)`, diagnostic: "unknown error origin"},
		{name: "goto", source: `func f() error { e := source(); goto done; done: if e != nil { return e }; return nil }`, diagnostic: "goto control flow is unsupported"},
		{name: "named return", source: `func f() (e error) { e = source(); if e != nil { return }; return }`, diagnostic: "implicit or tuple"},
		{name: "unique alias", source: `var errhancefmt int; func f() error { e := source(); if e != nil { return e }; return nil }`, count: 1, want: `errhancefmt2.Errorf("source: %w", e)`},
		{name: "reader caller", source: `type reader struct{}; func (reader) Read([]byte) (int, error) { return 0, source() }; func f() error { _, e := (reader{}).Read(nil); if e != nil { return e }; return nil }`, diagnostic: "sentinel"},
		{name: "reader implementation", source: `type reader struct{}; func (reader) ReadByte() (byte, error) { e := source(); if e != nil { return 0, e }; return 0, nil }`, diagnostic: "sentinel"},
		{name: "indirect reader", source: `type reader struct{}; func (reader) ReadRune() (rune, int, error) { return 0, 0, source() }; func f() error { read := (reader{}).ReadRune; _, _, e := read(); if e != nil { return e }; return nil }`, diagnostic: "indirect call"},
		{name: "function named Read", source: `func Read() error { return source() }; func f() error { e := Read(); if e != nil { return e }; return nil }`, count: 1, want: `Errorf("Read: %w", e)`},
		{name: "non-reader method caller", source: `type reader struct{}; func (reader) Read(string) error { return source() }; func f() error { e := (reader{}).Read(""); if e != nil { return e }; return nil }`, count: 1, want: `Errorf("(reader{}).Read: %w", e)`},
		{name: "non-reader method implementation", source: `type reader struct{}; func (reader) ReadByte(int) error { e := source(); if e != nil { return e }; return nil }`, count: 1, want: `Errorf("source: %w", e)`},
	}
	imp := importer.Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := base + tt.source
			if tt.name == "lookalike error" {
				src = "package p\n" + tt.source
			}
			typedTestRewrite(t, imp, src, SourceConfig{overwrite: tt.overwrite}, tt.count, tt.want, tt.diagnostic)
		})
	}
}

func TestRewriteTypedPackageTerminator(t *testing.T) {
	imp := importer.Default()
	for _, prefix := range []string{
		"package p; ",
		"// Package p documents p.\npackage p;\n",
		"package p // package comment\n",
		"package p /* multiline\npackage comment */\n",
		"package p /* multiline\r\npackage comment */\r\n",
		"package p /* comment */; ",
		"package p; // package comment\n",
		"package p; /* multiline\npackage comment */\n",
	} {
		t.Run(prefix, func(t *testing.T) {
			src := prefix + `func source() error { return nil }; func f() error { err := source(); if err != nil { return err }; return nil }`
			typedTestRewrite(t, imp, src, SourceConfig{}, 1, `Errorf("source: %w", err)`, "")
			fset, file, info := typedTestCheck(t, imp, src)
			after, _, _, err := rewriteTyped(fset, file, info, src, SourceConfig{})
			if err != nil || !strings.HasPrefix(after, strings.TrimRight(prefix, " \r\n")) {
				t.Fatalf("package comments changed: %v\n%s", err, after)
			}
		})
	}
}

func TestRewriteTypedOriginLiteral(t *testing.T) {
	src := `package p
type receiver struct{}
func makeReceiver(string) receiver { return receiver{} }
func (receiver) Run() error { return nil }
func f() error {
 err := makeReceiver("quoted \"%s\"\n").Run()
 if err != nil { return err }; return nil
}`
	typedTestRewrite(t, importer.Default(), src, SourceConfig{}, 1, `makeReceiver(\"quoted \\\"%%s\\\"\\n\").Run: %w`, "")
	src = strings.ReplaceAll(src, `"quoted \"%s\"\n"`, "`multiline\n%s`")
	typedTestRewrite(t, importer.Default(), src, SourceConfig{}, 1, "makeReceiver(`multiline\\n%%s`).Run: %w", "")
}

func TestRewriteTypedFmtOtherFile(t *testing.T) {
	const src = `package p
import f "fmt"
var _ = f.Sprint
func source() error { return nil }
func run() error { err := source(); if err != nil { return err }; return nil }
`
	const other = `package p; import f "fmt"; var _ = f.Sprint`
	imp := importer.Default()
	fset, file, info := typedTestCheck(t, imp, src)
	second, err := parser.ParseFile(fset, "other.go", other, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	check := types.Config{Importer: imp}
	if _, err := check.Check("fixture", fset, []*ast.File{file, second}, info); err != nil {
		t.Fatal(err)
	}
	after, diagnostics, count, err := rewriteTyped(fset, file, info, src, SourceConfig{})
	if err != nil || count != 1 || !strings.Contains(after, `f.Errorf("source: %w", err)`) {
		t.Fatalf("other file fmt binding: count=%d diagnostics=%v err=%v\n%s", count, diagnostics, err, after)
	}
	typedTestCheck(t, imp, after)
}

func TestRewriteTypedBlockedFunctions(t *testing.T) {
	src := `package p
func source() error { return nil }
func f() error { err := source(); if err != nil { return err }; return nil }
type receiver struct{}
func (*receiver) Run() error { err := source(); if err != nil { return err }; return nil }
`
	typedTestRewrite(t, importer.Default(), src, SourceConfig{blockedFunctions: map[string]string{
		"fixture.f":               "test contract requires error identity",
		"(*fixture.receiver).Run": "test contract requires error identity",
	}}, 0, "", "test contract requires error identity")
}

func TestRewriteTypedNoOutOfScopeDiagnostics(t *testing.T) {
	src := `package p; func f() { for {} }; func g() int { for {}; return 0 }`
	fset, file, info := typedTestCheck(t, importer.Default(), src)
	_, diagnostics, count, err := rewriteTyped(fset, file, info, src, SourceConfig{})
	if err != nil || count != 0 || len(diagnostics) != 0 {
		t.Fatalf("non-error functions: count=%d diagnostics=%v err=%v", count, diagnostics, err)
	}
}

func TestRewriteTypedFmt(t *testing.T) {
	const base = "package p\nimport (\"errors\"; f \"fmt\")\nfunc source() error { return errors.New(\"failure\") }\n"
	tests := []struct {
		name, source, want, diagnostic string
		count                          int
		overwrite                      bool
	}{
		{name: "existing alias", source: `var _ = f.Sprint; func run() error { e := source(); if e != nil { return e }; return nil }`, count: 1, want: `f.Errorf("source: %w", e)`},
		{name: "existing wrap unchanged", source: `func run() error { e := source(); if e != nil { return f.Errorf("original: %w", e) }; return nil }`},
		{name: "indexed overwrite", source: `func run() error { e := source(); if e != nil { return f.Errorf("original %[2]*.[3]*[1]v / %[1]w / %%", e, 9, 2) }; return nil }`, overwrite: true, count: 1, want: `f.Errorf("source: original %[2]*.[3]*[1]v / %[1]w / %%", e, 9, 2)`},
		{name: "argument effects preserved", source: `func side() int { return 42 }; func run() error { e := source(); if e != nil { return f.Errorf("%d: %w", side(), e) }; return nil }`, overwrite: true, count: 1, want: `f.Errorf("source: %d: %w", side(), e)`},
		{name: "multiple wrapping directives", source: `func run() error { e := source(); if e != nil { return f.Errorf("%w and %[1]w", e) }; return nil }`, overwrite: true, count: 1, want: `"source: %w and %[1]w"`},
		{name: "variadic", source: `func run() error { e := source(); if e != nil { return f.Errorf("%w", []any{e}...) }; return nil }`, overwrite: true, diagnostic: "variadic"},
		{name: "nonliteral", source: `const format = "%w"; func run() error { e := source(); if e != nil { return f.Errorf(format, e) }; return nil }`, overwrite: true, diagnostic: "literal constant"},
		{name: "fmt shadow", source: `var _ = f.Sprint; func run() error { f := 1; _ = f; e := source(); if e != nil { return e }; return nil }`, diagnostic: "shadowed"},
		{name: "fake Errorf", source: `var _ = f.Sprint; type fake struct{}; func (fake) Errorf(string, ...any) error { return source() }; func run() error { f := fake{}; e := source(); if e != nil { return f.Errorf("%w", e) }; return nil }`, overwrite: true, diagnostic: "fmt import is shadowed"},
		{name: "no double wrap variable", source: `func run() error { e := f.Errorf("failure"); if e != nil { return e }; return nil }`, diagnostic: "already formatted"},
	}
	imp := importer.Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typedTestRewrite(t, imp, base+tt.source, SourceConfig{overwrite: tt.overwrite}, tt.count, tt.want, tt.diagnostic)
		})
	}
}

func TestRewriteTypedGenerated(t *testing.T) {
	src := "// Code generated by test; DO NOT EDIT.\npackage p\nimport \"errors\"\nfunc source() error { return errors.New(\"failure\") }; func f() error { e := source(); if e != nil { return e }; return nil }"
	imp := importer.Default()
	typedTestRewrite(t, imp, src, SourceConfig{}, 0, "", "")
	typedTestRewrite(t, imp, src, SourceConfig{generated: true}, 1, `Errorf("source: %w", e)`, "")
}

func TestRewriteTypedStdlibContracts(t *testing.T) {
	imp := importer.Default()
	typedTestRewrite(t, imp, `package p
import "io"
func f(r io.Reader) error { _, e := r.Read(nil); if e != nil { return e }; return nil }
`, SourceConfig{}, 0, "", "sentinel")
	typedTestRewrite(t, imp, `package p
import "bufio"
func f(r *bufio.Reader) error { _, e := r.ReadByte(); if e != nil { return e }; return nil }
`, SourceConfig{}, 0, "", "sentinel")
	typedTestRewrite(t, imp, `package p
import "io"
func f() error { e := io.EOF; if e != nil { return e }; return nil }
`, SourceConfig{}, 0, "", "unknown error origin")
	typedTestRewrite(t, imp, `package p
import (. "fmt"; "errors")
func source() error { return errors.New("failure") }
func f() error { e := source(); if e != nil { return Errorf("%[1]w", e) }; return nil }
`, SourceConfig{overwrite: true}, 1, `Errorf("source: %[1]w", e)`, "")
	typedTestRewrite(t, imp, `package p
import (_ "fmt"; "errors")
func source() error { return errors.New("failure") }
func f() error { e := source(); if e != nil { return e }; return nil }
`, SourceConfig{}, 0, "", "unsupported binding")
	typedTestRewrite(t, imp, `package p
import "errors"
func source() error { return errors.New("failure") }
func f() error { e := source(); defer func() { e = nil }(); if e != nil { return e }; return nil }
`, SourceConfig{}, 0, "", "captured error")
}

func TestRewriteTypedInvalidInput(t *testing.T) {
	if _, _, _, err := rewriteTyped(nil, nil, nil, "package p", SourceConfig{}); err == nil {
		t.Fatal("missing input accepted")
	}
	fset, file, info := typedTestCheck(t, importer.Default(), "package p")
	if _, _, _, err := rewriteTyped(fset, file, info, "package longer", SourceConfig{}); err == nil {
		t.Fatal("mismatched source accepted")
	}
}

func typedTestCheck(t *testing.T, imp types.Importer, src string) (*token.FileSet, *ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	// Ensure offsets are file-relative, not token.Pos-1.
	fset.AddFile("earlier.go", -1, 137)
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v\n%s", err, src)
	}
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue), Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object), Implicits: make(map[ast.Node]types.Object), Selections: make(map[*ast.SelectorExpr]*types.Selection), Scopes: make(map[ast.Node]*types.Scope), Instances: make(map[*ast.Ident]types.Instance)}
	cfg := types.Config{Importer: imp}
	if _, err := cfg.Check("fixture", fset, []*ast.File{file}, info); err != nil {
		t.Fatalf("typecheck fixture: %v\n%s", err, src)
	}
	return fset, file, info
}

func typedTestRewrite(t *testing.T, imp types.Importer, src string, cfg SourceConfig, count int, want, diagnostic string) {
	t.Helper()
	fset, file, info := typedTestCheck(t, imp, src)
	after, diags, got, err := rewriteTyped(fset, file, info, src, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != count {
		t.Fatalf("rewrites = %d, want %d; diagnostics: %v\n%s", got, count, diags, after)
	}
	if count == 0 && after != src {
		t.Fatal("no-op changed source")
	}
	if want != "" && !strings.Contains(after, want) {
		t.Fatalf("missing %q in:\n%s", want, after)
	}
	if _, err := format.Source([]byte(after)); err != nil {
		t.Fatalf("format rewritten source: %v\n%s", err, after)
	}
	if diagnostic != "" {
		found := false
		for _, d := range diags {
			if !d.Position.IsValid() || d.Position.Filename != "fixture.go" {
				t.Fatalf("invalid diagnostic position: %v", d)
			}
			found = found || strings.Contains(d.Message, diagnostic)
		}
		if !found {
			t.Fatalf("missing diagnostic %q: %v", diagnostic, diags)
		}
	}
	// Original AST/info can be reused: the engine must not mutate either.
	again, _, n, err := rewriteTyped(fset, file, info, src, cfg)
	if err != nil || again != after || n != got {
		t.Fatalf("rewrite mutated input AST/info: %v", err)
	}
	fset, file, info = typedTestCheck(t, imp, after)
	again, _, n, err = rewriteTyped(fset, file, info, after, cfg)
	if err != nil || again != after || n != 0 {
		t.Fatalf("rewrite not idempotent: count=%d err=%v\n%s", n, err, again)
	}
}
