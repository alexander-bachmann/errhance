package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

type Config struct{}

func Do(config Config, src string) (string, error) {
	for {
		// recompute AST after each replacement
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return src, fmt.Errorf("parser.ParseFile: %w", err)
		}
		var ok bool
		src, ok = replace(file, src)
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

func replace(file *ast.File, src string) (string, bool) {
	hasReplaced := false
	latestWrappedErr := ""

	ast.Inspect(file, func(node ast.Node) bool {
		if hasReplaced {
			return Skip
		}
		if node == nil {
			return Next
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			latestWrappedErr = wrappedErr(*n)
		case *ast.IfStmt:
			var ok bool
			src, ok = replaceWithWrappedErr(*n, src, latestWrappedErr)
			if !ok {
				break
			}
			hasReplaced = true
			return Skip
		}
		// fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End())])
		return Next
	})
	return src, hasReplaced
}

func wrappedErr(assignStmt ast.AssignStmt) string {
	if !returnsErr(assignStmt) {
		return ""
	}
	name, ok := callsFunc(assignStmt)
	if !ok {
		return ""
	}
	return "fmt.Errorf(\"" + name + ": %w\", err)"
}

func returnsErr(assignStmt ast.AssignStmt) bool {
	if ident, ok := assignStmt.Lhs[len(assignStmt.Lhs)-1].(*ast.Ident); ok {
		return ident.Name == "err"
	}
	return false
}

func callsFunc(assignStmt ast.AssignStmt) (string, bool) {
	for _, rhs := range assignStmt.Rhs {
		switch n := rhs.(type) {
		case *ast.CallExpr:
			return funcName(*n), true
		}
	}
	return "", false
}

func funcName(callExpr ast.CallExpr) string {
	switch fun := callExpr.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if obj, ok := fun.X.(*ast.Ident); ok {
			return obj.Name + "." + fun.Sel.Name
		}
		return fun.Sel.Name
	}
	return ""
}

func replaceWithWrappedErr(ifStmt ast.IfStmt, src, newErr string) (string, bool) {
	if newErr == "" {
		return src, false
	}
	if !isErrNilCheck(ifStmt) {
		return src, false
	}
	pos := returnErrPos(ifStmt)
	if pos == -1 {
		return src, false
	}
	return src[:pos-1] + newErr + src[pos+2:], true
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

func returnErrPos(ifStmt ast.IfStmt) int {
	if ifStmt.Body == nil {
		return -1
	}
	for _, stmt := range ifStmt.Body.List {
		returnStmt, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, result := range returnStmt.Results {
			ident, ok := result.(*ast.Ident)
			if !ok || ident.Name != "err" {
				continue
			}
			return int(ident.Pos())
		}
	}
	return -1
}
