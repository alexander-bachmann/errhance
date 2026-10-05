package errhance

import (
	"go/importer"
	"testing"
)

func TestRewriteTypedFlow(t *testing.T) {
	const base = `package p
func source() error { return nil }
func other() error { return nil }
`
	tests := []struct {
		name, source, want string
		count              int
	}{
		{"for local", `func f(b bool) error { for b { e := source(); if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"loop carried nil", `func f(b bool) error { e := source(); if e != nil { for b { if b { return e }; e = nil } }; return nil }`, "", 0},
		{"loop carried origin", `func f(b bool) error { e := source(); for b { if e != nil { return e }; e = other() }; return nil }`, "", 0},
		{"init reset post", `func f(b bool) error { e := source(); if e != nil { for e = nil; b; e = other() { return e } }; return nil }`, "", 0},
		{"nonnull init nil post", `func f(b bool) error { e := source(); if e != nil { for e = e; b; e = nil { if b { return e } } }; return nil }`, "", 0},
		{"post clears body guard", `func f(b bool) error { e := source(); if e != nil { for ; b; e = nil { if b { return e } } }; return nil }`, "", 0},
		{"loop stable outer", `func f(b bool) error { e := source(); if e != nil { for b { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"loop fresh outer assignment", `func f(b bool) error { var e error; for b { e = source(); if e != nil { return e }; e = nil }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"loop exit unknown", `func f(b bool) error { e := source(); for b { e = other(); break }; if e != nil { return e }; return nil }`, "", 0},
		{"range local", `func f(xs []int) error { for range xs { e := source(); if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"range writes value", `func f(xs []error) error { e := source(); if e != nil { for _, e = range xs { return e } }; return nil }`, "", 0},
		{"range writes body", `func f(xs []int, b bool) error { e := source(); if e != nil { for range xs { if b { return e }; e = nil } }; return nil }`, "", 0},
		{"range shadow", `func f(xs []error) error { e := source(); if e != nil { for _, e := range xs { if e != nil { return e } }; return e }; return nil }`, "", 0},
		{"continue guard", `func f(xs []int) error { for range xs { e := source(); if e == nil { continue }; return e }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"break guard", `func f(b bool) error { for b { e := source(); if e == nil { break }; return e }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"unreachable after continue", `func f(b bool) error { for b { e := source(); if e != nil { continue; return e } }; return nil }`, "", 0},
		{"labeled loop", `func f(b bool) error { outer: for b { e := source(); if e == nil { continue outer }; return e }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"labeled break", `func f(b bool) error { outer: for b { e := source(); if e == nil { break outer }; return e }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"switch locals", `func f(n int) error { switch n { case 0: e := source(); if e != nil { return e }; case 1: e := other(); if e != nil { return e } }; return nil }`, `Errorf("other: %w", e)`, 2},
		{"switch init fresh guard", `func f() error { switch e := source(); { case e != nil: if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"switch nil no guess", `func f() error { switch e := source(); e { case nil: return e }; return nil }`, "", 0},
		{"switch break guard", `func f(n int) error { switch n { case 0: e := source(); if e == nil { break }; return e }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"fallthrough invalidates", `func f(n int) error { e := source(); if e != nil { switch n { case 0: e = nil; fallthrough; case 1: return e } }; return nil }`, "", 0},
		{"fallthrough fresh call", `func f(n int) error { var e error; switch n { case 0: e = nil; fallthrough; case 1: e = source(); if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"nested switch break", `func f(n int) error { e := source(); if e != nil { switch n { case 0: switch n { case 0: e = nil; break }; return e } }; return nil }`, "", 0},
		{"nested switch fresh", `func f(n int) error { switch n { case 0: switch n { default: break }; e := source(); if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"nested switch fallthrough", `func f(n int) error { e := source(); if e != nil { switch n { case 0: switch n { default: e = nil; break }; fallthrough; default: return e } }; return nil }`, "", 0},
		{"type switch local", `func f(x any) error { switch x := x.(type) { case error: if x != nil { return x }; default: e := source(); if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"select receive unknown", `func f(ch chan error) error { select { case e := <-ch: if e != nil { return e }; default: }; return nil }`, "", 0},
		{"select overwrite outer", `func f(ch chan error) error { e := source(); if e != nil { select { case e = <-ch: return e; default: } }; return nil }`, "", 0},
		{"select local", `func f(ch chan error) error { select { case <-ch: e := source(); if e != nil { return e }; default: }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"select break guard", `func f(ch chan error) error { select { case <-ch: e := source(); if e == nil { break }; return e }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"loop parallel assignment", `func f(b bool) error { for b { a, e := source(), other(); a, e = e, a; if e != nil { return e }; _ = a }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"loop fake nil", `func f(b bool) error { nil := other(); for b { e := source(); if e != nil { return e } }; return nil }`, "", 0},
		{"closure reads only", `func f(b bool) error { for b { e := source(); read := func() { _ = e }; read(); if e != nil { return e } }; return nil }`, `Errorf("source: %w", e)`, 1},
		{"closure user case", `func f(b bool) error { for b { err := source(); if err != nil { _ = func() error { if err != nil { return err }; local := other(); if local != nil { return local }; return nil }; return err } }; return nil }`, `Errorf("other: %w", local)`, 2},
		{"closure address escape", `func f() error { e := source(); g := func() *error { return &e }; _ = g; if e != nil { return e }; return nil }`, "", 0},
		{"closure range mutation", `func f(xs []error) error { e := source(); g := func() { for _, e = range xs {} }; g(); if e != nil { return e }; return nil }`, "", 0},
		{"closure shadow write", `func f() error { e := source(); g := func() { e := other(); e = nil; _ = e }; g(); if e != nil { return e }; return nil }`, `Errorf("source: %w", e)`, 1},
	}
	imp := importer.Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typedTestRewrite(t, imp, base+tt.source, SourceConfig{}, tt.count, tt.want, "")
		})
	}
}
