package errhance

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestHasRewriteCandidate(t *testing.T) {
	const base = "package p\nfunc source() error { return nil }\n"
	tests := []struct {
		name, src string
		cfg       SourceConfig
		want      bool
	}{
		{"new error", base + `func f() error { e := source(); if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"alias", base + `func f() error { e := source(); a := e; if a != nil { return a }; return nil }`, SourceConfig{}, true},
		{"alias after guard", base + `func f() error { e := source(); if nil == e { return nil }; var a = e; b := a; return b }`, SourceConfig{}, true},
		{"parentheses", base + `func f() error { var e = (source()); if (nil) != (e) { return (e) }; return nil }`, SourceConfig{}, true},
		{"tuple", `package p; func pair() (int, error) { return 0, nil }; func f() error { _, e := pair(); if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"for condition", base + `func f() error { e := source(); for e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"loop local", base + `func f(b bool) error { for b { e := source(); if e == nil { continue }; return e }; return nil }`, SourceConfig{}, true},
		{"range local", base + `func f(xs []int) error { for range xs { e := source(); if e != nil { return e } }; return nil }`, SourceConfig{}, true},
		{"switch local", base + `func f(b bool) error { switch b { case true: e := source(); if e != nil { return e } }; return nil }`, SourceConfig{}, true},
		{"select local", base + `func f(ch chan int) error { select { case <-ch: e := source(); if e != nil { return e }; default: }; return nil }`, SourceConfig{}, true},
		{"branch union", base + `func f(b bool) error { var e error; if b { e = source() }; if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"closure local", base + `func f() func() error { return func() error { e := source(); if e != nil { return e }; return nil } }`, SourceConfig{}, true},
		{"closure capture", base + `func f() func() error { e := source(); return func() error { if e != nil { return e }; return nil } }`, SourceConfig{}, false},
		{"closure read preserves outer", base + `func f() error { e := source(); _ = func() { _ = e }; if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"closure facts isolated", base + `func f() error { var e error; _ = func() { e = source() }; if e != nil { return e }; return nil }`, SourceConfig{}, false},
		{"functions isolated", base + `func f() { e := source(); _ = e }; func g(e error) error { if e != nil { return e }; return nil }`, SourceConfig{}, false},
		{"scope shadow", base + `func f(e error) error { { e := source(); _ = e }; if e != nil { return e }; return nil }`, SourceConfig{}, false},
		{"shadow nil", base + `func f() error { nil := source(); e := source(); if e != nil { return e }; return nil }`, SourceConfig{}, false},
		{"crossfile nil uncertain", `package p; func f() Failure { e := source(); if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"external alias", `package p; func f() external.Error { e := source(); if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"conversion allowed", `package p; func f() error { e := error(nil); if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"unresolved binding", `package p; func f() error { if e != nil { return e }; return nil }`, SourceConfig{}, true},
		{"no call", base + `func f(e error) error { if e != nil { return e }; return nil }`, SourceConfig{}, false},
		{"no guard", base + `func f() error { e := source(); return e }`, SourceConfig{}, false},
		{"scalar", `package p; func source() int { return 1 }; func f() int { x := source(); if x != 0 { return x }; return 0 }`, SourceConfig{}, false},
		{"named return", base + `func f() (e error) { e = source(); if e != nil { return }; return }`, SourceConfig{}, false},
		{"direct call", base + `func f() error { return source() }`, SourceConfig{}, false},
		{"pointer", base + `func f() *error { e := source(); if e != nil { return &e }; return nil }`, SourceConfig{}, false},
		{"selector", base + `func f() error { e := source(); s := struct{ E error }{e}; if e != nil { return s.E }; return nil }`, SourceConfig{}, false},
		{"wrapped", `package p; import "fmt"; func f() error { return fmt.Errorf("wrapped") }`, SourceConfig{}, false},
		{"wrapped with boolean helper", `package p; import "fmt"; func f() error { return fmt.Errorf("wrapped") }; func g() bool { return true }`, SourceConfig{}, false},
		{"wrapped guarded", `package p; import "fmt"; func f() error { e := fmt.Errorf("wrapped"); if e != nil { return fmt.Errorf("context: %w", e) }; return nil }`, SourceConfig{}, false},
		{"overwrite", `package p; import f "fmt"; func run() error { return f.Errorf("wrapped") }`, SourceConfig{overwrite: true}, true},
		{"overwrite dot", `package p; import . "fmt"; func run() error { return Errorf("wrapped") }`, SourceConfig{overwrite: true}, true},
		{"local fmt shadow", base + `func f() error { fmt := struct{ Errorf func() error }{source}; e := fmt.Errorf(); if e != nil { return e }; return nil }`, SourceConfig{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hasRewriteCandidate(tt.src, tt.cfg)
			if err != nil || got != tt.want {
				t.Fatalf("hasRewriteCandidate = %v, %v; want %v, nil", got, err, tt.want)
			}
		})
	}
}

func TestHasRewriteCandidateGeneratedAndInvalid(t *testing.T) {
	const src = "// Code generated by test. DO NOT EDIT.\npackage p\nfunc f() error { e := source(); if e != nil { return e }; return nil }"
	for _, generated := range []bool{false, true} {
		cfg := SourceConfig{generated: generated}
		got, err := hasRewriteCandidate(src, cfg)
		if err != nil || got != generated {
			t.Fatalf("generated=%v: got %v, %v", generated, got, err)
		}
		for _, invalid := range []string{"package p; func broken(", src + "\nfunc broken("} {
			if _, err := hasRewriteCandidate(invalid, cfg); err == nil {
				t.Fatal("parse error suppressed")
			}
		}
	}
}

// Exhaust a bounded fixture space against Source, including both actual edits
// and no-ops. Type errors must fail the test rather than vacuously pass it.
func TestHasRewriteCandidateSourceProperty(t *testing.T) {
	bodies := []string{
		`e := CALL; if e != nil { return e }; return nil`,
		`e := CALL; if nil == e { return nil }; a := e; return a`,
		`var e error; if b { e = CALL } else { e = CALL }; if e != nil { return e }; return nil`,
		`e := CALL; for e != nil { return e }; return nil`,
		`for b { e := CALL; if e == nil { continue }; return e }; return nil`,
		`e := CALL; g := func() error { if e != nil { return e }; return nil }; return g()`,
		`g := func() error { e := CALL; if e != nil { return e }; return nil }; return g()`,
		`e := CALL; if e != nil { return fmt.Errorf("context: %w", e) }; return nil`,
		`e := CALL; return e`,
		`return CALL`,
		`var e error; if e != nil { return e }; return nil`,
		`e := CALL; { e := error(nil); if e != nil { return e } }; _ = e; return nil`,
		`nil := CALL; e := CALL; if e != nil { return e }; return nil`,
	}
	for i, body := range bodies {
		for _, call := range []string{"source()", `fmt.Errorf("already wrapped")`, "error(nil)"} {
			for _, overwrite := range []bool{false, true} {
				for _, generated := range []bool{false, true} {
					t.Run(fmt.Sprintf("%d/%s/overwrite=%v/generated=%v", i, call, overwrite, generated), func(t *testing.T) {
						src := "package p\nimport \"fmt\"\nvar _ = fmt.Errorf\ntype Failure = error\nfunc source() Failure { return nil }\nfunc f(b bool) Failure { " + strings.ReplaceAll(body, "CALL", call) + " }"
						if generated {
							src = "// Code generated by test. DO NOT EDIT.\n" + src
						}
						cfg := SourceConfig{overwrite: overwrite, generated: overwrite}
						candidate, err := hasRewriteCandidate(src, cfg)
						if err != nil {
							t.Fatal(err)
						}
						report, err := Source(cfg, src)
						if err != nil && !errors.Is(err, ErrNoChanges) {
							t.Fatal(err)
						}
						if !candidate && (report.After != src || report.TotalErrorsEnhanced != 0) {
							t.Fatalf("false negative: %s\nafter: %s", src, report.After)
						}
					})
				}
			}
		}
	}
}

func TestHasRewriteCandidateCrossFileAlias(t *testing.T) {
	const dependency = `package p; type Failure = error; func source() Failure { return nil }`
	const src = `package p; func f() Failure { e := source(); if e != nil { return e }; return nil }`
	got, err := hasRewriteCandidate(src, SourceConfig{})
	if err != nil || !got {
		t.Fatalf("cross-file alias: got %v, %v", got, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for i, text := range []string{dependency, src} {
		file, err := parser.ParseFile(fset, fmt.Sprintf("file%d.go", i), text, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue), Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object), Implicits: make(map[ast.Node]types.Object),
		Scopes: make(map[ast.Node]*types.Scope), Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	var cfg types.Config
	if _, err := cfg.Check("p", fset, files, info); err != nil {
		t.Fatal(err)
	}
	after, _, count, err := rewriteTyped(fset, files[1], info, src, SourceConfig{})
	if err != nil || count != 1 || after == src {
		t.Fatalf("cross-file fixture must rewrite: count=%d, err=%v", count, err)
	}
}
