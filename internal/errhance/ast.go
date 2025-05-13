package errhance

import (
	"go/ast"
	"go/token"
	"strings"
)

// Nearly all of errhance works with the ast package, but
// this file is specifically for functions that know nothing
// of errhance's intention.
// For testing these types of functions, it is suggested to use
// the helper function castSnippet, see ast_test.go for examples

// The error is expected to be the last returned value.
// e.g. want 'foo, bar, err := Baz()', not 'foo, err, bar := Baz()'
func returnsErr(assignStmt ast.AssignStmt) bool {
	ident, ok := assignStmt.Lhs[len(assignStmt.Lhs)-1].(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "err"
}

// err := a.b.c().d() => a.b.c.d
// err := a(b(c(), d()) => a
func callExprName(assignStmt ast.AssignStmt) string {
	// TODO: add support for 'err = <-errCh'
	var name string
	// TODO: when does an assignStmt have > 1 Rhs?
	for _, rhs := range assignStmt.Rhs {
		switch n := rhs.(type) {
		case *ast.CallExpr:
			name = strings.TrimSuffix(callExprNext(*n, ""), ".")
		}
	}
	return name
}

func callExprNext(callExpr ast.CallExpr, name string) string {
	switch f := callExpr.Fun.(type) {
	case *ast.Ident:
		name += f.Name
	case *ast.SelectorExpr:
		if f.Sel != nil {
			name += selectorExprNext(*f, name) + f.Sel.Name
		} else {
			name += selectorExprNext(*f, name)
		}
	}
	return appendDot(name)
}

func selectorExprNext(selectorExpr ast.SelectorExpr, name string) string {
	switch x := selectorExpr.X.(type) {
	case *ast.Ident:
		name += x.Name
	case *ast.CallExpr:
		name += callExprNext(*x, name)
	case *ast.SelectorExpr:
		if x.Sel != nil {
			name += selectorExprNext(*x, name) + x.Sel.Name
		} else {
			name += selectorExprNext(*x, name)
		}
	}
	return appendDot(name)
}

func appendDot(name string) string {
	if len(name) > 0 && name[len(name)-1] != '.' {
		return name + "."
	}
	return name
}

func hasErrNotEqualNilCond(ifStmt ast.IfStmt) bool {
	binExpr, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if binExpr.Op != token.NEQ {
		return false
	}
	ident, ok := binExpr.X.(*ast.Ident)
	if !ok {
		return false
	}
	if ident.Name != "err" {
		return false
	}
	return true
}

func isFmtErrorf(callExpr ast.CallExpr) bool {
	selectorExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := selectorExpr.X.(*ast.Ident)
	if !ok {
		return false
	}
	if pkgIdent.Name != "fmt" || selectorExpr.Sel.Name != "Errorf" {
		return false
	}
	return true
}
