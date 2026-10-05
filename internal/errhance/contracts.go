package errhance

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
)

// Keys survive independent package/test variants and generic instantiation.
func contractFunctionKey(fn *types.Func) string { return fn.Origin().FullName() }

func contractLiteralKey(fset *token.FileSet, fn *ast.FuncLit) string {
	p := fset.PositionFor(fn.Pos(), false)
	return fmt.Sprintf("literal:%s:%d", p.Filename, p.Offset)
}

type contractNode struct {
	key string
	obj types.Object
}

func contractObjectNode(obj types.Object) contractNode {
	// Imported globals and test variants have distinct objects but shared storage.
	if obj != nil && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
		return contractNode{key: "var:" + obj.Pkg().Path() + "." + obj.Name()}
	}
	return contractNode{obj: obj}
}

// projectContracts follows a flow-insensitive union of assignments backwards.
// Extra blocking is intentional: branches, loops and reassignments must never
// hide a producer. Only root syntax is inspected, not external consumers.
func projectContracts(pkgs []*packages.Package) (map[string]string, []Diagnostic) {
	blocked := make(map[string]string)
	edges := make(map[contractNode]map[contractNode]bool)
	known := make(map[contractNode]bool)
	unknown := make(map[contractNode]bool)
	callables := make(map[string]bool)
	seeds := make(map[contractNode]Diagnostic)
	var diagnostics []Diagnostic
	seen := make(map[Diagnostic]bool)
	addDiagnostic := func(d Diagnostic) {
		if !seen[d] {
			seen[d] = true
			diagnostics = append(diagnostics, d)
		}
	}
	link := func(to contractNode, from []contractNode) {
		if edges[to] == nil {
			edges[to] = make(map[contractNode]bool)
		}
		for _, source := range from {
			edges[to][source] = true
		}
	}
	errType := types.Universe.Lookup("error").Type()
	for _, pkg := range pkgs {
		info := pkg.TypesInfo
		if info == nil {
			continue
		}
		mayBeError := func(expr ast.Expr) bool {
			t := info.TypeOf(expr)
			if t == nil || t == types.Typ[types.UntypedNil] {
				return false
			}
			// Boxing does not remove identity/type contracts on dynamic errors.
			_, iface := t.Underlying().(*types.Interface)
			return iface || types.AssignableTo(t, errType)
		}
		isNil := func(expr ast.Expr) bool {
			id, ok := ast.Unparen(expr).(*ast.Ident)
			return ok && info.ObjectOf(id) == types.Universe.Lookup("nil")
		}
		unresolved := func(expr ast.Expr) []contractNode {
			p := pkg.Fset.PositionFor(expr.Pos(), false)
			n := contractNode{key: fmt.Sprintf("unknown:%s:%d", p.Filename, p.Offset)}
			unknown[n] = true
			return []contractNode{n}
		}
		var provenance func(ast.Expr) []contractNode
		provenance = func(expr ast.Expr) []contractNode {
			switch e := ast.Unparen(expr).(type) {
			case *ast.Ident:
				switch obj := info.ObjectOf(e).(type) {
				case *types.Func:
					return []contractNode{{key: contractFunctionKey(obj)}}
				case *types.Var:
					return []contractNode{contractObjectNode(obj)}
				default:
					return nil
				}
			case *ast.FuncLit:
				return []contractNode{{key: contractLiteralKey(pkg.Fset, e)}}
			case *ast.SelectorExpr:
				if fn, ok := info.Uses[e.Sel].(*types.Func); ok {
					// Interface method objects are not concrete implementations.
					if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
						if _, ok := recv.Type().Underlying().(*types.Interface); ok {
							return unresolved(e)
						}
					}
					if sel := info.Selections[e]; sel != nil {
						if _, ok := sel.Recv().Underlying().(*types.Interface); ok {
							return unresolved(e)
						}
					}
					return []contractNode{{key: contractFunctionKey(fn)}}
				}
				if obj, ok := info.Uses[e.Sel].(*types.Var); ok && info.Selections[e] == nil {
					return []contractNode{contractObjectNode(obj)}
				}
				return unresolved(e)
			case *ast.CallExpr:
				if id, ok := ast.Unparen(e.Fun).(*ast.Ident); ok && info.Uses[id] == types.Universe.Lookup("recover") {
					return unresolved(e)
				}
				var out []contractNode
				if !info.Types[e.Fun].IsType() {
					out = append(out, provenance(e.Fun)...)
				}
				// Arguments may be forwarded, including error parameters and callbacks.
				for _, arg := range e.Args {
					t := info.TypeOf(arg)
					if t != nil {
						_, callable := t.Underlying().(*types.Signature)
						if mayBeError(arg) || callable || info.Types[e.Fun].IsType() {
							out = append(out, provenance(arg)...)
						}
					}
				}
				return out
			case *ast.IndexExpr:
				if _, ok := info.TypeOf(e).(*types.Signature); ok {
					if id, ok := ast.Unparen(e.X).(*ast.Ident); ok {
						if _, ok := info.Uses[id].(*types.Func); ok {
							return provenance(e.X)
						}
					}
					if sel, ok := ast.Unparen(e.X).(*ast.SelectorExpr); ok {
						if _, ok := info.Uses[sel.Sel].(*types.Func); ok {
							return provenance(e.X)
						}
					}
				}
				return unresolved(e)
			case *ast.IndexListExpr:
				return provenance(e.X)
			case *ast.TypeAssertExpr:
				return provenance(e.X)
			case *ast.BasicLit:
				return nil
			default:
				return unresolved(e)
			}
		}
		assign := func(lhs, rhs []ast.Expr) {
			for i, expr := range lhs {
				var obj types.Object
				switch e := ast.Unparen(expr).(type) {
				case *ast.Ident:
					obj = info.ObjectOf(e)
				case *ast.SelectorExpr:
					if info.Selections[e] == nil {
						obj = info.Uses[e.Sel]
					}
				}
				if obj == nil {
					continue
				}
				n := contractObjectNode(obj)
				known[n] = true
				if len(lhs) == len(rhs) {
					link(n, provenance(rhs[i]))
				} else if len(rhs) == 1 {
					link(n, provenance(rhs[0]))
				}
			}
		}
		warn := func(n ast.Node, message string, operands ...ast.Expr) {
			d := Diagnostic{Position: pkg.Fset.PositionFor(n.Pos(), false), Message: message}
			addDiagnostic(d)
			for _, expr := range operands {
				for _, source := range provenance(expr) {
					seeds[source] = d
				}
			}
		}
		function := func(key string, ft *ast.FuncType, body *ast.BlockStmt, reader bool) {
			n := contractNode{key: key}
			callables[key] = true
			if reader {
				seeds[n] = Diagnostic{Position: pkg.Fset.PositionFor(ft.Pos(), false), Message: "Read/ReadByte/ReadRune method may require sentinel error identity"}
			}
			if ft.Params != nil {
				for _, field := range ft.Params.List {
					for _, id := range field.Names {
						unknown[contractObjectNode(info.ObjectOf(id))] = true
					}
				}
			}
			var results []*ast.Ident
			if ft.Results != nil {
				for _, field := range ft.Results.List {
					if len(field.Names) == 0 {
						results = append(results, nil)
					}
					for _, id := range field.Names {
						results = append(results, id)
						result := contractObjectNode(info.ObjectOf(id))
						known[result] = true
						link(n, []contractNode{result})
					}
				}
			}
			if body == nil {
				unknown[n] = true
				return
			}
			ast.Inspect(body, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					return false
				}
				if ret, ok := node.(*ast.ReturnStmt); ok {
					for i, expr := range ret.Results {
						from := provenance(expr)
						link(n, from)
						// Explicit returns assign named results before deferred code runs.
						if len(ret.Results) == len(results) {
							if results[i] != nil {
								link(contractObjectNode(info.ObjectOf(results[i])), from)
							}
						} else {
							for _, id := range results {
								if id != nil {
									link(contractObjectNode(info.ObjectOf(id)), from)
								}
							}
						}
					}
				}
				return true
			})
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.FuncDecl:
					if fn, ok := info.Defs[n.Name].(*types.Func); ok {
						function(contractFunctionKey(fn), n.Type, n.Body, n.Recv != nil && typedReadName(n.Name.Name))
					}
				case *ast.FuncLit:
					function(contractLiteralKey(pkg.Fset, n), n.Type, n.Body, false)
				case *ast.AssignStmt:
					assign(n.Lhs, n.Rhs)
				case *ast.RangeStmt:
					// Range overwrites existing bindings too; collection/iterator
					// element provenance is not modeled by ordinary assignments.
					for _, expr := range []ast.Expr{n.Key, n.Value} {
						var obj types.Object
						switch e := ast.Unparen(expr).(type) {
						case *ast.Ident:
							obj = info.ObjectOf(e)
						case *ast.SelectorExpr:
							if info.Selections[e] == nil {
								obj = info.Uses[e.Sel]
							}
						}
						if obj != nil {
							unknown[contractObjectNode(obj)] = true
						}
					}
				case *ast.UnaryExpr:
					if n.Op == token.AND {
						if id, ok := ast.Unparen(n.X).(*ast.Ident); ok {
							unknown[contractObjectNode(info.ObjectOf(id))] = true
						}
					}
				case *ast.ValueSpec:
					lhs := make([]ast.Expr, len(n.Names))
					for i, id := range n.Names {
						lhs[i] = id
					}
					assign(lhs, n.Values)
				case *ast.BinaryExpr:
					if (n.Op == token.EQL || n.Op == token.NEQ) && !isNil(n.X) && !isNil(n.Y) && (mayBeError(n.X) || mayBeError(n.Y)) {
						warn(n, "direct error identity comparison may change after wrapping; use errors.Is for wrapping compatibility", n.X, n.Y)
					}
				case *ast.SwitchStmt:
					if n.Tag != nil {
						for _, stmt := range n.Body.List {
							for _, expr := range stmt.(*ast.CaseClause).List {
								if !isNil(expr) && !isNil(n.Tag) && (mayBeError(n.Tag) || mayBeError(expr)) {
									warn(expr, "error expression switch uses direct identity; use errors.Is for wrapping compatibility", n.Tag, expr)
								}
							}
						}
					}
				case *ast.TypeAssertExpr:
					if mayBeError(n.X) {
						message := "direct error type assertion may fail after wrapping; use errors.As for wrapping compatibility"
						if n.Type == nil {
							message = "error type switch may change after wrapping; use errors.As for wrapping compatibility"
						}
						warn(n, message, n.X)
					}
				}
				return true
			})
		}
	}
	// Reachability computes the least fixed point, including recursive functions
	// and cyclic aliases. Sort seeds so diagnostic reasons stay deterministic.
	var queue []contractNode
	for n := range seeds {
		queue = append(queue, n)
	}
	sort.Slice(queue, func(i, j int) bool {
		a, b := seeds[queue[i]], seeds[queue[j]]
		if a.Position.Filename != b.Position.Filename {
			return a.Position.Filename < b.Position.Filename
		}
		if a.Position.Offset != b.Position.Offset {
			return a.Position.Offset < b.Position.Offset
		}
		return a.Message < b.Message
	})
	visited := make(map[contractNode]bool)
	for i := 0; i < len(queue); i++ {
		n := queue[i]
		if visited[n] {
			continue
		}
		visited[n] = true
		d := seeds[n]
		if callables[n.key] {
			blocked[n.key] = d.Message
		}
		if unknown[n] || (n.obj != nil && !known[n]) {
			d.Message = "unresolved error identity contract; conservatively skipping all loaded functions for wrapping compatibility"
			addDiagnostic(d)
			for key := range callables {
				blocked[key] = d.Message
			}
		}
		for source := range edges[n] {
			if _, exists := seeds[source]; !exists {
				seeds[source] = d
			}
			queue = append(queue, source)
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Position.Filename != b.Position.Filename {
			return a.Position.Filename < b.Position.Filename
		}
		if a.Position.Offset != b.Position.Offset {
			return a.Position.Offset < b.Position.Offset
		}
		return a.Message < b.Message
	})
	return blocked, diagnostics
}
