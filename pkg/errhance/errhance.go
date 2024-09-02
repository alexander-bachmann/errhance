package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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
	imports := make(map[string]struct{})
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
		case *ast.GenDecl:
			collectImports(*n, imports)
		case *ast.AssignStmt:
			latestWrappedErr = wrappedErr(*n, imports)
		case *ast.IfStmt:
			var ok bool
			src, ok = replaceWithWrappedErr(*n, src, latestWrappedErr)
			if !ok {
				break
			}
			hasReplaced = true
			return Skip
		}
		// fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End()-1)])
		return Next
	})
	return src, hasReplaced
}

func collectImports(genDecl ast.GenDecl, imports map[string]struct{}) {
	if genDecl.Tok != token.IMPORT {
		return
	}
	for _, spec := range genDecl.Specs {
		if spec, ok := spec.(*ast.ImportSpec); ok {
			imports[stripPackage(spec.Path.Value)] = struct{}{}
		}
	}
}

func stripPackage(pkg string) string {
	// path/filepath => filepath
	pkg = pkg[strings.LastIndex(pkg, "/")+1 : len(pkg)-1]
	pkg = strings.ReplaceAll(pkg, "\"", "")
	return pkg
}

func wrappedErr(assignStmt ast.AssignStmt, imports map[string]struct{}) string {
	if !returnsErr(assignStmt) {
		return ""
	}
	name, ok := callsFunc(assignStmt, imports)
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

func callsFunc(assignStmt ast.AssignStmt, imports map[string]struct{}) (string, bool) {
	for _, rhs := range assignStmt.Rhs {
		switch n := rhs.(type) {
		case *ast.CallExpr:
			return funcName(*n, imports), true
		}
	}
	return "", false
}

func funcName(callExpr ast.CallExpr, imports map[string]struct{}) string {
	var name string
	switch fun := callExpr.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		if obj, ok := fun.X.(*ast.Ident); ok {
			// support functions such as os.Read()
			if _, ok = imports[obj.Name]; ok {
				name = obj.Name + "." + fun.Sel.Name
			} else {
				// don't support methods such as b.Baz()
				name = fun.Sel.Name
			}
		}
	}
	return name
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
