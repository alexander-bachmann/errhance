package errhance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestContractsRelevantBindingIndices(t *testing.T) {
	for _, consumer := range []string{
		`_, e := forward(1, producer()); return e == E(1)`,
		`_, e := forward(pair()); return e == E(1)`,
		`f := factory(1, pair); _, e := f(); return e == E(1)`,
		`f := receiver.forward; _, e := f(receiver{}, 1, producer()); return e == E(1)`,
	} {
		t.Run(consumer, func(t *testing.T) {
			const declarations = `package p
type E int
func (E) Error() string { return "error" }
func producer() E { return E(1) }
func pair() (int, E) { return 1, producer() }
func forward(n int, e E) (count int, err E) { return n, e }
func factory(n int, f func() (int, E)) func() (int, E) { return f }
type receiver struct{}
func (receiver) forward(n int, e E) (int, E) { return n, e }
func independent() E { return E(2) }
`
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "bindings.go", declarations+`func consume() bool { `+consumer+` }`, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{
				Types: make(map[ast.Expr]types.TypeAndValue), Defs: make(map[*ast.Ident]types.Object),
				Uses: make(map[*ast.Ident]types.Object), Selections: make(map[*ast.SelectorExpr]*types.Selection),
			}
			cfg := types.Config{}
			pkg, err := cfg.Check("example.com/p", fset, []*ast.File{file}, info)
			if err != nil {
				t.Fatal(err)
			}
			blocked, diagnostics := projectContracts([]*packages.Package{{Fset: fset, Syntax: []*ast.File{file}, Types: pkg, TypesInfo: info}})
			if blocked["example.com/p.producer"] == "" || blocked["example.com/p.independent"] != "" || len(diagnostics) == 0 {
				t.Fatalf("wrong contracts after scalar bindings: %v, %+v", blocked, diagnostics)
			}
		})
	}
}
