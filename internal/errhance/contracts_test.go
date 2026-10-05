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

func TestSourceContractsRangeBoxingAndRecover(t *testing.T) {
	const producer = `package p
import "io"
func source() error { return io.EOF }
func producer() error { e := source(); if e != nil { return e }; return nil }
`
	baseline, err := Source(NewSourceConfig(), producer)
	if err != nil || baseline.TotalErrorsEnhanced != 1 {
		t.Fatalf("producer must be eligible without consumers: %+v, %v", baseline, err)
	}
	for _, tc := range []struct {
		name, consumer, diagnostic string
		fallback                   bool
	}{
		{"range error value", `func consume() bool { var e error; for _, e = range []error{producer()} {}; return e == io.EOF }`, "direct error identity", true},
		{"range error key", `func consume() bool { var e error; for e = range map[error]bool{producer(): true} {}; return e == io.EOF }`, "direct error identity", true},
		{"range function value", `func consume() bool { var f func() error; for _, f = range []func() error{producer} {}; return f() == io.EOF }`, "direct error identity", true},
		{"range boxed value", `func consume() bool { var x any; for _, x = range []any{producer()} {}; return x == any(io.EOF) }`, "direct error identity", true},
		{"boxed comparison", `func consume() bool { var x any = producer(); return x == any(io.EOF) }`, "direct error identity", false},
		{"boxed inequality", `func consume() bool { var x any = producer(); return any(io.EOF) != x }`, "direct error identity", false},
		{"boxed concrete counterpart", `func consume() bool { var x any = producer(); return x == 42 }`, "direct error identity", false},
		{"boxed assertion", `func consume() { var x any = producer(); _, _ = x.(interface { Timeout() bool }) }`, "direct error type assertion", false},
		{"boxed type switch", `func consume() { var x any = producer(); switch x.(type) { case interface { Timeout() bool }: } }`, "error type switch", false},
		{"boxed expression switch", `func consume() { var x any = producer(); switch x { case any(io.EOF): } }`, "error expression switch", false},
		{"generic boxing", `func box[T any](v T) any { return any(v) }; func consume() bool { return box(producer()) == any(io.EOF) }`, "direct error identity", true},
		{"recover", `func consume() (ok bool) { defer func() { ok = recover() == io.EOF }(); panic(producer()) }`, "direct error identity", true},
		{"recover boxed", `func consume() (ok bool) { defer func() { x := (recover)(); ok = x == any(io.EOF) }(); panic(producer()) }`, "direct error identity", true},
		{"shadowed recover", `func recover() error { return producer() }; func consume() bool { return recover() == io.EOF }`, "direct error identity", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := producer + tc.consumer + "\n"
			report, err := Source(NewSourceConfig(), src)
			if !errors.Is(err, ErrNoChanges) || report.After != src || report.TotalErrorsEnhanced != 0 {
				t.Fatalf("identity producer changed: %+v, %v", report, err)
			}
			var consumerWarning, fallback bool
			for _, d := range report.Diagnostics {
				consumerWarning = consumerWarning || (d.Position.Line >= 5 && strings.Contains(d.Message, tc.diagnostic))
				fallback = fallback || strings.Contains(d.Message, "unresolved error identity contract")
			}
			if !consumerWarning || fallback != tc.fallback {
				t.Fatalf("consumer warning=%v, fallback=%v: %+v", consumerWarning, fallback, report.Diagnostics)
			}
		})
	}
}

func TestProjectContracts(t *testing.T) {
	const src = `package p
import "errors"
var sentinel = errors.New("sentinel")
func first() error { return nil }
func second() error { return nil }
func shadow() error { return nil }
func asserted() error { return nil }
func nilOnly() error { return nil }
func tuple() (int, error) { return 0, nil }
func generic[T any]() error { return nil }
type receiver struct{}
func (receiver) method() error { return nil }
func consumer(flag bool) {
 unknown := first()
 e := first()
 if flag { e = second() }
 if e == sentinel {}
 { e := shadow(); _ = e }
 e = nil
 var a = asserted()
 _, _ = a.(interface { Error() string })
 n := nilOnly(); if n != (nil) {}
 if nil == n {}
 _, e = tuple(); if sentinel != e {}
 if generic[int]() == sentinel {}
 if (receiver{}).method() != sentinel {}
 if unknown == sentinel {}
 _, _ = unknown.(interface { Error() string })
 if 1 == 2 {}
 switch unknown.(type) { default: }
}
`
	load := func() *packages.Package {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "contracts.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue), Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object), Selections: make(map[*ast.SelectorExpr]*types.Selection)}
		cfg := types.Config{Importer: importer.Default()}
		pkg, err := cfg.Check("example.com/p", fset, []*ast.File{file}, info)
		if err != nil {
			t.Fatal(err)
		}
		return &packages.Package{Fset: fset, Syntax: []*ast.File{file}, Types: pkg, TypesInfo: info}
	}
	// Independent type identities model ordinary and test package variants.
	first, variant := load(), load()
	blocked, diagnostics := projectContracts([]*packages.Package{first, variant})
	if len(diagnostics) != 8 {
		t.Fatalf("want 8 deduplicated warnings, got %+v", diagnostics)
	}
	for _, name := range []string{"first", "second", "asserted", "tuple", "generic"} {
		fn := first.Types.Scope().Lookup(name).(*types.Func)
		key := contractFunctionKey(fn)
		if blocked[key] == "" || key != contractFunctionKey(variant.Types.Scope().Lookup(name).(*types.Func)) {
			t.Errorf("missing or unstable key: %s", key)
		}
	}
	if blocked["(example.com/p.receiver).method"] == "" {
		t.Errorf("method not blocked: %v", blocked)
	}
	for _, name := range []string{"shadow", "nilOnly"} {
		if blocked[contractFunctionKey(first.Types.Scope().Lookup(name).(*types.Func))] != "" {
			t.Errorf("unrelated function %s blocked", name)
		}
	}
	for _, d := range diagnostics {
		if d.Position.Filename != "contracts.go" || d.Position.Line == 0 || !strings.Contains(d.Message, "wrapping compatibility") {
			t.Errorf("invalid diagnostic: %+v", d)
		}
	}
}

func TestProjectContractsPreserveEligibleProducers(t *testing.T) {
	const producers = `package p
import "errors"
var sentinel = errors.New("sentinel")
func source() error { return sentinel }
func producer() error { e := source(); if e != nil { return e }; return nil }
func independent() error { e := source(); if e != nil { return e }; return nil }
`
	for _, tc := range []struct {
		name, consumer string
		fallback       bool
	}{
		{"alias", `func consume() { e := producer(); alias := e; if alias == sentinel {} }`, false},
		{"alias cycle", `func consume(flag bool) { e := producer(); a := e; if flag { e = a }; if a != sentinel {} }`, false},
		{"forwarding", `func forward() error { return producer() }; func next() error { return forward() }; func consume() { if next() == sentinel {} }`, false},
		{"forwarding local", `func forward() error { e := producer(); alias := e; return alias }; func consume() { if forward() == sentinel {} }`, false},
		{"named result", `func forward() (err error) { err = producer(); return }; func consume() { if forward() == sentinel {} }`, false},
		{"deferred named result", `func forward() (err error) { defer func() { if err == sentinel {} }(); return producer() }`, false},
		{"tuple named result", `func pair() (int, error) { return 0, producer() }; func forward() (n int, err error) { defer func() { if err == sentinel {} }(); return pair() }`, false},
		{"reader", `type reader struct{}; func (reader) ReadByte() (byte, error) { return 0, producer() }`, false},
		{"reader Read", `type reader struct{}; func (reader) Read([]byte) (int, error) { return 0, producer() }`, false},
		{"reader ReadRune", `type reader struct{}; func (reader) ReadRune() (rune, int, error) { return 0, 0, producer() }`, false},
		{"expression switch", `func consume() { e := producer(); switch e { case nil: case sentinel: } }`, false},
		{"expression switch case", `func consume() { switch sentinel { case producer(): } }`, false},
		{"type switch", `func consume() { e := producer(); switch e.(type) { case interface { Timeout() bool }: } }`, false},
		{"type assertion", `func consume() { e := producer(); _, _ = e.(interface { Timeout() bool }) }`, false},
		{"function value", `func consume() { f := producer; g := f; if g() == sentinel {} }`, false},
		{"function returned", `func factory() func() error { return producer }; func consume() { if factory()() == sentinel {} }`, false},
		{"method value", `type getter struct{}; func (getter) Get() error { return producer() }; func consume() { f := (getter{}).Get; if f() == sentinel {} }`, false},
		{"generic", `func forward[T any]() error { return producer() }; func consume() { if forward[int]() == sentinel {} }`, false},
		{"literal", `func consume() { f := func() error { e := producer(); if e != nil { return e }; return nil }; if f() == sentinel {} }`, false},
		{"literal immediate", `func consume() { if (func() error { e := producer(); if e != nil { return e }; return nil })() == sentinel {} }`, false},
		{"interface dispatch", `type getter interface { Get() error }; type impl struct{}; func (impl) Get() error { return producer() }; func consume(g getter) { if g.Get() == sentinel {} }`, true},
		{"promoted interface dispatch", `type getter interface { Get() error }; type impl struct{ getter }; func consume(g impl) { if g.Get() == sentinel {} }`, true},
		{"unknown function", `func consume(f func() error) { if f() == sentinel {} }; func use() { consume(producer) }`, true},
		{"unknown error", `func consume(e error) { if e == sentinel {} }; func use() { consume(producer()) }`, true},
		{"field", `func consume() { box := struct { err error }{producer()}; if box.err == sentinel {} }`, true},
		{"pointer mutation", `func consume() { var e error; ptr := &e; *ptr = producer(); if e == sentinel {} }`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := projectFixture(t, map[string]string{"producer.go": producers, "consumer.go": "package p\n" + tc.consumer})
			report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
			if err != nil {
				t.Fatal(err)
			}
			got := readProjectFile(t, filepath.Join(root, "producer.go"))
			// Both functions are normally eligible. Only independent may change.
			start := strings.Index(got, "func producer()")
			end := strings.Index(got, "func independent()")
			if strings.Contains(got[start:end], "Errorf") {
				t.Fatalf("identity producer rewritten:\n%s", got)
			}
			consumer := readProjectFile(t, filepath.Join(root, "consumer.go"))
			if strings.Contains(consumer, "Errorf") {
				t.Fatalf("forwarder/literal rewritten:\n%s", consumer)
			}
			want := 1
			if tc.fallback {
				want = 0
			}
			if report.TotalErrorsEnhanced != want {
				t.Fatalf("want %d rewrites, got %+v", want, report)
			}
			fallback := false
			for _, d := range report.Diagnostics {
				fallback = fallback || strings.Contains(d.Message, "unresolved error identity contract")
			}
			if fallback != tc.fallback {
				t.Fatalf("fallback=%v, diagnostics=%+v", fallback, report.Diagnostics)
			}
		})
	}
}

func TestProjectContractsImportedEligibleProducer(t *testing.T) {
	const producer = `package p
import "io"
var Shared error
func source() error { return io.EOF }
func F() error { e := source(); if e != nil { return e }; return nil }
`
	root := projectFixture(t, map[string]string{
		"p.go": producer,
		"consumer/c.go": `package consumer
import ("example.com/project"; "io")
func consume() { p.Shared = p.F(); alias := p.Shared; if alias == io.EOF {} }
`,
		"p_test.go": `package p; import "io"; func consume() { if F() != io.EOF {} }`,
	})
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := readProjectFile(t, filepath.Join(root, "p.go")); got != producer || report.TotalErrorsEnhanced != 0 {
		t.Fatalf("imported producer changed: %+v\n%s", report, got)
	}
	for _, d := range report.Diagnostics {
		if strings.Contains(d.Message, "unresolved") {
			t.Fatalf("resolved imported contract triggered fallback: %+v", d)
		}
	}
}

func TestProjectContractsImportedProducer(t *testing.T) {
	root := projectFixture(t, map[string]string{
		"p.go": `package p; func F() error { return nil }`,
		"consumer/c.go": `package consumer
import ("example.com/project"; "io")
func consume() { err := p.F(); if err == io.EOF {} }
`,
		"p_test.go": `package p; import "io"; func consume() { if F() != io.EOF {} }`,
	})
	pkgs, err := packages.Load(projectConfig(context.Background(), root), "./...")
	if err != nil {
		t.Fatal(err)
	}
	if err := projectErrors(pkgs, &DirectoryReport{}); err != nil {
		t.Fatal(err)
	}
	blocked, diagnostics := projectContracts(pkgs)
	if blocked["example.com/project.F"] == "" || len(diagnostics) != 2 {
		t.Fatalf("imported/test consumer missing: %v, %+v", blocked, diagnostics)
	}
	report, err := LocalDirectory(projectDirectory(root), NewSourceConfig())
	if err != nil {
		t.Fatal(err)
	}
	var warnings int
	for _, d := range report.Diagnostics {
		if strings.Contains(d.Message, "direct error identity") {
			warnings++
		}
	}
	if warnings != 2 {
		t.Fatalf("project warnings missing: %+v", report.Diagnostics)
	}
	sr, _ := LocalFile(NewSourceConfig(), filepath.Join(root, "consumer/c.go"))
	if len(sr.Diagnostics) == 0 || !strings.Contains(sr.Diagnostics[0].Message, "wrapping compatibility") {
		t.Fatalf("source warning missing: %+v", sr)
	}
}
