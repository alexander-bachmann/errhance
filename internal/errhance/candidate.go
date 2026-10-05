package errhance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// hasRewriteCandidate is a syntax-only, overapproximating no-op filter for an
// explicit single-file save. False rules out an edit by the current typed engine;
// true still requires normal package loading, validation, and rewriting. It does
// not validate types or contracts; a no-op is not a project audit.
func hasRewriteCandidate(src string, cfg SourceConfig) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "source.go", src, parser.ParseComments)
	if err != nil {
		return false, err
	}
	if ast.IsGenerated(file) && !cfg.generated {
		return false, nil
	}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path == "C" {
			return true, nil // Preserve explicit-target diagnostics for cgo mappings.
		}
	}

	candidate := false
	ast.Inspect(file, func(n ast.Node) bool {
		if candidate {
			return false
		}
		var body *ast.BlockStmt
		switch fn := n.(type) {
		case *ast.FuncDecl:
			body = fn.Body
		case *ast.FuncLit:
			body = fn.Body
		default:
			return true
		}
		if body == nil {
			return true
		}

		parents := make(map[*ast.Object]*ast.Object)
		var root func(*ast.Object) *ast.Object
		root = func(obj *ast.Object) *ast.Object {
			if parent := parents[obj]; parent != nil && parent != obj {
				parents[obj] = root(parent)
				return parents[obj]
			}
			return obj
		}
		object := func(expr ast.Expr) *ast.Object {
			id, ok := ast.Unparen(expr).(*ast.Ident)
			if !ok || id.Name == "_" {
				return nil
			}
			if id.Obj == nil {
				// Unresolved names may come from another file or a dot import.
				// Local shadows have objects; builtin constants cannot carry origins.
				if id.Name != "nil" && id.Name != "true" && id.Name != "false" {
					candidate = true
				}
				return nil
			}
			if id.Obj.Pos() < n.Pos() || id.Obj.Pos() >= body.End() {
				return nil // Captures and package variables have no local origin.
			}
			return id.Obj
		}
		var calls, guards, returns []*ast.Object
		assign := func(lhs, rhs []ast.Expr) {
			for i, expr := range lhs {
				left := object(expr)
				if left == nil || len(rhs) == 0 {
					continue
				}
				var value ast.Expr
				if len(lhs) == len(rhs) {
					value = rhs[i]
				} else if len(rhs) == 1 {
					value = rhs[0] // A tuple call can supply any LHS slot.
				} else {
					candidate = true
					continue
				}
				switch ast.Unparen(value).(type) {
				case *ast.CallExpr:
					// Conversions and already-formatted calls are harmless false positives.
					calls = append(calls, left)
				case *ast.Ident:
					if right := object(value); right != nil {
						parents[root(left)] = root(right)
					}
				}
			}
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncLit:
				return false // The outer walk analyzes each closure with fresh facts.
			case *ast.AssignStmt:
				assign(node.Lhs, node.Rhs)
			case *ast.ValueSpec:
				lhs := make([]ast.Expr, len(node.Names))
				for i, name := range node.Names {
					lhs[i] = name
				}
				assign(lhs, node.Values)
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					break
				}
				x, y := ast.Unparen(node.X), ast.Unparen(node.Y)
				isNil := func(expr ast.Expr) bool {
					id, ok := expr.(*ast.Ident)
					return ok && id.Name == "nil" && id.Obj == nil
				}
				if isNil(x) {
					x, y = y, x
				}
				if isNil(y) {
					if obj := object(x); obj != nil {
						guards = append(guards, obj)
					}
				}
			case *ast.ReturnStmt:
				for _, expr := range node.Results {
					switch ast.Unparen(expr).(type) {
					case *ast.CallExpr:
						// Binding fmt.Errorf (including dot imports) requires types.
						candidate = candidate || cfg.overwrite
					case *ast.Ident:
						if obj := object(expr); obj != nil {
							returns = append(returns, obj)
						}
					}
				}
			}
			return true
		})
		// Union is deliberately flow-insensitive: branch order, reassignment, and
		// aliases copied after a guard cannot erase a possible typed rewrite.
		origins, guarded := make(map[*ast.Object]bool), make(map[*ast.Object]bool)
		for _, obj := range calls {
			origins[root(obj)] = true
		}
		for _, obj := range guards {
			guarded[root(obj)] = true
		}
		for _, obj := range returns {
			if origins[root(obj)] && guarded[root(obj)] {
				candidate = true
			}
		}
		return true
	})
	return candidate, nil
}
