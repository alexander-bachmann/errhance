package errhance

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestContractsNamedResultOwner(t *testing.T) {
	for _, tc := range []struct {
		name, function string
		identity       bool
	}{
		{"deferred identity", `func f() (same bool, err error) {
 defer func() { same = err == io.EOF }()
 err = source(); if err != nil { return false, err }; return false, nil
}`, true},
		{"deferred alias", `func f() (same bool, err error) {
 defer func() { var x any = err; same = x == any(io.EOF) }()
 err = source(); if err != nil { return false, err }; return false, nil
}`, true},
		{"deferred assertion", `func f() (same bool, err error) {
 defer func() { _, same = err.(interface { Timeout() bool }) }()
 err = source(); if err != nil { return false, err }; return false, nil
}`, true},
		{"closure owner", `func f() (bool, error) {
 g := func() (same bool, err error) {
  defer func() { same = err == io.EOF }()
  err = source(); if err != nil { return false, err }; return false, nil
 }
 alias := g; return alias()
}`, true},
		{"ordinary read-only capture", `func f() (same bool, err error) {
 defer func() { same = err != nil }()
 err = source(); if err != nil { return false, err }; return false, nil
}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `package p
import "io"
func source() error { return io.EOF }
` + tc.function + `
func independent() error { e := source(); if e != nil { return e }; return nil }
`
			report, err := Source(NewSourceConfig(), src)
			want := 2
			if tc.identity {
				want = 1
			}
			if err != nil || report.TotalErrorsEnhanced != want {
				t.Fatalf("want %d rewrites: %+v, %v", want, report, err)
			}
			start, end := strings.Index(report.After, "func f()"), strings.Index(report.After, "func independent()")
			if strings.Contains(report.After[start:end], "Errorf") == tc.identity ||
				!strings.Contains(report.After[end:], "Errorf") {
				t.Fatalf("wrong owner blocked:\n%s", report.After)
			}
		})
	}
}

func TestContractsResultSlots(t *testing.T) {
	const pair = `func pair() (any, error) { return "text", producer() };`
	for _, tc := range []struct {
		name, functions, consumer string
		conflict                  bool
	}{
		{"first slot", pair, `x, _ := pair(); _ = x.(string)`, false},
		{"second slot", pair, `_, e := pair(); var x any = e; _, _ = x.(interface { Timeout() bool })`, true},
		{"second boxed slot", `func pair() (any, any) { return "text", producer() };`, `_, x := pair(); _, _ = x.(interface { Timeout() bool })`, true},
		{"named slots", `func pair() (x any, err error) { return "text", producer() };`, `x, _ := pair(); _ = x.(string)`, false},
		{"naked named slots", `func pair() (x any, err error) { x = "text"; err = producer(); return };`, `x, _ := pair(); _ = x.(string)`, false},
		{"forwarded tuple", pair + `func forward() (any, error) { return pair() };`, `x, _ := forward(); _ = x.(string)`, false},
		{"forwarded named tuple", pair + `func forward() (x any, err error) { return pair() };`, `x, _ := forward(); _ = x.(string)`, false},
		{"forwarded error", pair + `func forward() (x any, err error) { return pair() };`, `_, e := forward(); if e == io.EOF {}`, true},
		{"function aliases", pair, `f := pair; g := f; x, _ := g(); _ = x.(string)`, false},
		{"function aliases error", pair, `f := pair; g := f; _, e := g(); if e == io.EOF {}`, true},
		{"returned function", pair + `func factory() func() (any, error) { return pair };`, `f := factory(); g := f; x, _ := g(); _ = x.(string)`, false},
		{"returned function error", pair + `func factory() func() (any, error) { return pair };`, `_, e := factory()(); if e == io.EOF {}`, true},
		{"returned closure", `func factory() func() (any, error) { return func() (any, error) { return "text", producer() } };`, `f := factory(); x, _ := f(); _ = x.(string)`, false},
		{"returned closure error", `func factory() func() (any, error) { return func() (any, error) { return "text", producer() } };`, `f := factory(); _, e := f(); if e == io.EOF {}`, true},
		{"swapped slots", pair + `func swap() (error, any) { x, e := pair(); return e, x };`, `_, x := swap(); _ = x.(string)`, false},
		{"local closure", ``, `f := func() (any, error) { return "text", producer() }; g := f; x, _ := g(); _ = x.(string)`, false},
		{"local closure error", ``, `f := func() (any, error) { return "text", producer() }; g := f; _, e := g(); if e == io.EOF {}`, true},
		{"inherited method", `type base struct{}; func (base) pair() (any, error) { return "text", producer() }; type derived struct { base };`, `f := (derived{}).pair; x, _ := f(); _ = x.(string)`, false},
		{"inherited method error", `type base struct{}; func (base) pair() (any, error) { return "text", producer() }; type derived struct { base };`, `f := (derived{}).pair; _, e := f(); if e == io.EOF {}`, true},
		{"named owner no feedback", `func pair() (x any, err error) { defer func() { _ = x.(string) }(); return "text", producer() };`, `x, _ := pair(); _ = x.(string)`, false},
		{"identity owner no sibling feedback", `func pair() (x any, err error) { defer func() { _, _ = x.(error) }(); x = producer(); err = independent(); return };`, `_, _ = pair()`, true},
		{"parameter first slot", `func pair(e error) (any, error) { return "text", e };`, `x, _ := pair(producer()); _ = x.(string)`, false},
		{"parameter second slot", `func pair(e error) (any, error) { return "text", e };`, `_, e := pair(producer()); var x any = e; _, _ = x.(interface { Timeout() bool })`, true},
		{"forwarded parameter first slot", `func pair(e error) (any, error) { return "text", e }; func forward(e error) (any, error) { return pair(e) };`, `x, _ := forward(producer()); _ = x.(string)`, false},
		{"forwarded parameter second slot", `func pair(e error) (any, error) { return "text", e }; func forward(e error) (any, error) { return pair(e) };`, `_, e := forward(producer()); if e == io.EOF {}`, true},
		{"callback parameter first slot", `func pair(f func() error) (any, error) { return "text", f() };`, `x, _ := pair(producer); _ = x.(string)`, false},
		{"callback parameter second slot", `func pair(f func() error) (any, error) { return "text", f() };`, `_, e := pair(producer); if e == io.EOF {}`, true},
		{"returned parameter callback", `func factory(f func() (any, error)) func() (any, error) { return f };` + pair, `f := factory(pair); x, _ := f(); _ = x.(string)`, false},
		{"returned parameter callback error", `func factory(f func() (any, error)) func() (any, error) { return f };` + pair, `f := factory(pair); _, e := f(); if e == io.EOF {}`, true},
		{"method expression alias first slot", `type base struct{}; func (base) pair(e error) (any, error) { return "text", e }; type derived struct { base };`, `f := derived.pair; x, _ := f(derived{}, producer()); _ = x.(string)`, false},
		{"method expression alias second slot", `type base struct{}; func (base) pair(e error) (any, error) { return "text", e }; type derived struct { base };`, `f := derived.pair; _, e := f(derived{}, producer()); if e == io.EOF {}`, true},
		{"tuple arguments first slot", pair + `func forward(x any, err error) (any, error) { return x, err };`, `x, _ := forward(pair()); _ = x.(string)`, false},
		{"tuple arguments second slot", pair + `func forward(x any, err error) (any, error) { return x, err };`, `_, e := forward(pair()); if e == io.EOF {}`, true},
		{"named callback no owner feedback", `func factory() (f func() error, err error) { f = producer; err = independent(); return };`, `f, _ := factory(); if f() == io.EOF {}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `package p
import "io"
func source() error { return io.EOF }
func producer() error { e := source(); if e != nil { return e }; return nil }
func independent() error { e := source(); if e != nil { return e }; return nil }
` + tc.functions + `func consume() { ` + tc.consumer + ` }`
			report, err := Source(NewSourceConfig(), src)
			want := 2
			if tc.conflict {
				want = 1
			}
			if err != nil || report.TotalErrorsEnhanced != want {
				t.Fatalf("want %d rewrites: %+v, %v", want, report, err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "slots.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue), Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object), Selections: make(map[*ast.SelectorExpr]*types.Selection)}
			cfg := types.Config{Importer: importer.Default()}
			pkg, err := cfg.Check("example.com/p", fset, []*ast.File{file}, info)
			if err != nil {
				t.Fatal(err)
			}
			blocked, diagnostics := projectContracts([]*packages.Package{{Fset: fset, Syntax: []*ast.File{file}, Types: pkg, TypesInfo: info}})
			if (blocked["example.com/p.producer"] != "") != tc.conflict || blocked["example.com/p.independent"] != "" {
				t.Fatalf("wrong return contract targets: %v", blocked)
			}
			if blocked["example.com/p.factory"] != "" {
				t.Fatalf("callback result contract leaked into factory: %v", blocked)
			}
			if !tc.conflict && (len(blocked) != 0 || len(diagnostics) != 0) {
				t.Fatalf("non-error slot contaminated: %v, %+v", blocked, diagnostics)
			}
		})
	}
}

func TestContractsRecursiveResultProjection(t *testing.T) {
	const src = `package p
import "io"
type next func() (next, error)
func recursive() (next, error) { f, _ := recursive(); return f() }
func consume() { _, e := recursive(); if e == io.EOF {} }
func source() error { return io.EOF }
func independent() error { e := source(); if e != nil { return e }; return nil }
`
	report, err := Source(NewSourceConfig(), src)
	if err != nil || report.TotalErrorsEnhanced != 1 {
		t.Fatalf("recursive contract suppressed unrelated producer: %+v, %v", report, err)
	}
	for _, d := range report.Diagnostics {
		if strings.Contains(d.Message, "unresolved error identity contract") {
			return
		}
	}
	t.Fatalf("missing recursive provenance warning: %+v", report.Diagnostics)
}
