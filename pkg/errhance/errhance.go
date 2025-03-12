package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func containsOmitComment(src string) bool {
	omitComments := []string{
		"noeh", "no eh",
		"noerrhance", "no errhance",
	}
	for _, comment := range omitComments {
		if strings.Contains(src, "//") && strings.Contains(src, comment) {
			return true
		}
	}
	return false
}

func File(src string, cfg Config) (string, error) {
	for {
		// recompute AST after each replacement
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return src, fmt.Errorf("parser.ParseFile: %w", err)
		}
		var ok bool
		src, ok = replace(file, src, cfg)
		if !ok {
			break
		}
	}
	return src, nil
}

const (
	Next = true
	Skip = false
)

func replace(file *ast.File, src string, cfg Config) (string, bool) {
	hasReplaced := false
	nearestWrappedErr := ""

	ast.Inspect(file, func(node ast.Node) bool {
		if hasReplaced {
			return Skip
		}
		if node == nil {
			return Next
		}
		switch n := node.(type) {
		case *ast.AssignStmt: // err := foo()
			nearestWrappedErr = generateWrappedErr(*n) // fmt.Errorf("foo: %w", err)
		case *ast.IfStmt: // if err != nil
			if containsOmitComment(src[int((*n).Pos())-1 : int((*n).End()-1)]) {
				return Skip
			}
			var ok bool
			// return err => return fmt.Errorf("foo: %w", err)
			src, ok = replaceWithWrappedErr(*n, src, nearestWrappedErr, cfg.overwrite)
			if !ok {
				break
			}
			hasReplaced = true
			return Skip
		}
		// don't delete -- this is very helpful
		fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End()-1)])
		return Next
	})
	return src, hasReplaced
}

func generateWrappedErr(assignStmt ast.AssignStmt) string {
	if !returnsErr(assignStmt) {
		return ""
	}
	name, ok := callsFunc(assignStmt)
	if !ok || name == "" {
		return ""
	}
	return "fmt.Errorf(\"" + name + ": %w\", err)"
}

func returnsErr(assignStmt ast.AssignStmt) bool {
	// expect err to be the last return value
	// 'foo, bar, err := Baz()', not 'foo, err, bar := Baz()'
	if ident, ok := assignStmt.Lhs[len(assignStmt.Lhs)-1].(*ast.Ident); ok {
		return ident.Name == "err"
	}
	return false
}

func callsFunc(assignStmt ast.AssignStmt) (string, bool) {
	for _, rhs := range assignStmt.Rhs {
		switch n := rhs.(type) {
		case *ast.CallExpr:
			return callTrace(*n), true
		}
	}
	return "", false
}

// a.b.c().d() => a.b.c.d
// a(b(c(), d()) => a
func callTrace(callExpr ast.CallExpr) string {
	return strings.TrimSuffix(callExprNext(callExpr, ""), ".")
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

func replaceWithWrappedErr(ifStmt ast.IfStmt, src, newErr string, overwrite bool) (string, bool) {
	if newErr == "" {
		return src, false
	}
	if !isErrNilCheck(ifStmt) {
		return src, false
	}
	pos, len := returnErrPosLen(ifStmt, overwrite)
	if pos == -1 || len == -1 {
		return src, false
	}
	// if ifStmt has an assignStmt, it means it uses the shorthand err check syntax
	// e.g. if err := foo(); err != nil {}
	switch a := ifStmt.Init.(type) {
	case *ast.AssignStmt:
		newErr = generateWrappedErr(*a)
	}
	// make no changes if current err is what errhance generates
	if src[pos-1:pos+len-1] == newErr {
		return src, false
	}
	return src[:pos-1] + newErr + src[pos+len-1:], true
}

func isErrNilCheck(ifStmt ast.IfStmt) bool {
	binExpr, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if binExpr.Op != token.NEQ {
		return false
	}
	if ident, ok := binExpr.X.(*ast.Ident); !ok || ident.Name != "err" {
		return false
	}
	return true
}

func returnErrPosLen(ifStmt ast.IfStmt, overwrite bool) (int, int) {
	pos := -1
	len := -1
	if ifStmt.Body == nil {
		return pos, len
	}
	for _, stmt := range ifStmt.Body.List {
		returnStmt, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, result := range returnStmt.Results {
			switch r := result.(type) {
			case *ast.Ident:
				if (*r).Name == "err" {
					pos = int((*r).Pos())
					len = 3
					return pos, len
				}
			case *ast.CallExpr:
				if overwrite && isFmtErrorf(r) {
					pos = int((*r).Pos())
					len = int((*r).End() - (*r).Pos())
					return pos, len
				}
				if isFmtErrorf(r) {
				}
			}
		}
	}
	return pos, len
}

func isFmtErrorf(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkgIdent.Name == "fmt" && sel.Sel.Name == "Errorf"
}
